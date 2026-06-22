package handlers

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"paperless-api/internal/httpx"
)

// Stats godoc: GET /dashboard/stats
// Requires document_admin / system_admin / auditor.
// Aggregate document counts for the admin dashboard.
func (h *DocumentHandler) Stats(c *gin.Context) {
	ctx := c.Request.Context()

	// Pre-seed all known statuses so the dashboard tiles always render (0 if none).
	byStatus := map[string]int{"imported": 0, "pending": 0, "rejected": 0, "completed": 0, "cancelled": 0}
	if err := scanGroup(ctx, h, `SELECT status, count(*) FROM documents GROUP BY status`, byStatus); err != nil {
		h.log.Error("stats by status", zap.Error(err))
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "stats failed")
		return
	}

	bySync := map[string]int{"not_required": 0, "sync_pending": 0, "synced": 0, "sync_failed": 0, "sync_unknown": 0}
	if err := scanGroup(ctx, h, `SELECT COALESCE(sync_status,'not_required'), count(*) FROM documents GROUP BY 1`, bySync); err != nil {
		h.log.Error("stats by sync", zap.Error(err))
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "stats failed")
		return
	}

	type formatRow struct {
		DocFormatCode string `json:"doc_format_code"`
		Count         int    `json:"count"`
	}
	byFormat := []formatRow{}
	rows, err := h.pool.Query(ctx, `
		SELECT doc_format_code, count(*) FROM documents
		 GROUP BY doc_format_code ORDER BY count(*) DESC, doc_format_code LIMIT 20`)
	if err != nil {
		h.log.Error("stats by format", zap.Error(err))
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "stats failed")
		return
	}
	defer rows.Close()
	total := 0
	for rows.Next() {
		var r formatRow
		if err := rows.Scan(&r.DocFormatCode, &r.Count); err != nil {
			httpx.Error(c, http.StatusInternalServerError, "internal_error", "stats failed")
			return
		}
		byFormat = append(byFormat, r)
		total += r.Count
	}
	if err := rows.Err(); err != nil {
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "stats failed")
		return
	}

	httpx.OK(c, http.StatusOK, gin.H{
		"total":     total,
		"by_status": byStatus,
		"by_sync":   bySync,
		"by_format": byFormat,
	})
}

// scanGroup runs a 2-column (key text, count int) GROUP BY query and fills out.
func scanGroup(ctx context.Context, h *DocumentHandler, query string, out map[string]int) error {
	rows, err := h.pool.Query(ctx, query)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var k string
		var n int
		if err := rows.Scan(&k, &n); err != nil {
			return err
		}
		out[k] = n
	}
	return rows.Err()
}
