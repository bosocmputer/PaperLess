package handlers

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"paperless-api/internal/httpx"
	"paperless-api/internal/middleware"
	"paperless-api/internal/workflow"
)

type AuditHandler struct {
	pool   *pgxpool.Pool
	engine *workflow.Engine
}

func NewAuditHandler(pool *pgxpool.Pool, engine *workflow.Engine) *AuditHandler {
	return &AuditHandler{pool: pool, engine: engine}
}

// AuditLogs godoc: GET /documents/:id/audit-logs
// Returns the full timeline of who did what and when. Accessible to admin/auditor only.
// Never exposes tokens, passwords, or raw signature binary.
func (h *AuditHandler) AuditLogs(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	docID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_id", "document id must be an integer")
		return
	}

	if !hasRole(claims, "system_admin", "document_admin", "auditor", "workflow_admin") {
		httpx.Error(c, http.StatusForbidden, "forbidden", "insufficient role to view audit logs")
		return
	}

	ctx := c.Request.Context()

	// Audit log entries.
	type auditEntry struct {
		ID         int64   `json:"id"`
		ActorType  *string `json:"actor_type"`
		ActorID    *string `json:"actor_id"`
		Action     string  `json:"action"`
		EntityType string  `json:"entity_type"`
		EntityID   string  `json:"entity_id"`
		Reason     *string `json:"reason"`
		CreatedAt  string  `json:"created_at"`
	}
	rows, err := h.pool.Query(ctx, `
		SELECT id, actor_type, actor_id, action, entity_type, entity_id, reason, created_at::text
		  FROM audit_logs
		 WHERE entity_type='document' AND entity_id=$1
		 ORDER BY created_at
		 LIMIT 500
	`, strconv.FormatInt(docID, 10))
	if err != nil {
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "fetch failed")
		return
	}
	defer rows.Close()

	var entries []auditEntry
	for rows.Next() {
		var e auditEntry
		if err := rows.Scan(&e.ID, &e.ActorType, &e.ActorID, &e.Action, &e.EntityType, &e.EntityID, &e.Reason, &e.CreatedAt); err != nil {
			httpx.Error(c, http.StatusInternalServerError, "internal_error", "scan failed")
			return
		}
		entries = append(entries, e)
	}

	// Signature events (no token, no raw binary — only hash + metadata).
	type sigEvent struct {
		ID          int64   `json:"id"`
		TaskID      int64   `json:"task_id"`
		SignerType  *string `json:"signer_type"`
		SignerName  string  `json:"signer_name"`
		Action      string  `json:"action"`
		Comment     *string `json:"comment"`
		IPAddress   *string `json:"ip_address"`
		SignedAt    string  `json:"signed_at"`
	}
	sigRows, err := h.pool.Query(ctx, `
		SELECT id, task_id, signer_type, signer_name, action, comment,
		       host(ip_address) AS ip_str, signed_at::text
		  FROM signature_events
		 WHERE document_id=$1
		 ORDER BY signed_at
		 LIMIT 500
	`, docID)
	if err != nil {
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "signature events fetch failed")
		return
	}
	defer sigRows.Close()

	var sigEvents []sigEvent
	for sigRows.Next() {
		var e sigEvent
		if err := sigRows.Scan(&e.ID, &e.TaskID, &e.SignerType, &e.SignerName, &e.Action, &e.Comment, &e.IPAddress, &e.SignedAt); err != nil {
			httpx.Error(c, http.StatusInternalServerError, "internal_error", "signature events scan failed")
			return
		}
		sigEvents = append(sigEvents, e)
	}
	if entries == nil {
		entries = []auditEntry{}
	}
	if sigEvents == nil {
		sigEvents = []sigEvent{}
	}

	httpx.OK(c, http.StatusOK, gin.H{
		"audit_logs":       entries,
		"signature_events": sigEvents,
	})
}

// WorkflowStatus godoc: GET /documents/:id/workflow-status
// Returns per-step progress for the document (e.g. 1/2 signed for condition_type=2).
func (h *AuditHandler) WorkflowStatus(c *gin.Context) {
	docID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_id", "document id must be an integer")
		return
	}
	ctx := c.Request.Context()

	claims := middleware.ClaimsFrom(c)
	ok, err := canAccessDocument(ctx, h.pool, claims, docID)
	if err != nil {
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "fetch failed")
		return
	}
	if !ok {
		httpx.Error(c, http.StatusForbidden, "forbidden", "not authorized to view this document")
		return
	}

	progress, err := h.engine.StepProgressForDocument(ctx, docID)
	if err != nil {
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "fetch failed")
		return
	}
	if progress == nil {
		progress = []workflow.StepProgress{}
	}

	var totalSteps = len(progress)
	var completedSteps int
	for _, p := range progress {
		if p.Complete {
			completedSteps++
		}
	}

	httpx.OK(c, http.StatusOK, gin.H{
		"steps":           progress,
		"total_steps":     totalSteps,
		"completed_steps": completedSteps,
	})
}
