package handlers

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"paperless-api/internal/auth"
	"paperless-api/internal/httpx"
	"paperless-api/internal/middleware"
	"paperless-api/internal/pdf"
	"paperless-api/internal/storage"
	"paperless-api/internal/workflow"
)

type TaskHandler struct {
	pool   *pgxpool.Pool
	store  *storage.Client
	engine *workflow.Engine
	log    *zap.Logger
}

func NewTaskHandler(pool *pgxpool.Pool, store *storage.Client, engine *workflow.Engine, log *zap.Logger) *TaskHandler {
	return &TaskHandler{pool: pool, store: store, engine: engine, log: log}
}

// Inbox godoc: GET /signature-tasks/inbox
// Returns only open tasks assigned to the authenticated user. Server-side pagination.
func (h *TaskHandler) Inbox(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil {
		httpx.Error(c, http.StatusUnauthorized, "unauthorized", "not authenticated")
		return
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "20"))
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 100 {
		size = 20
	}
	offset := (page - 1) * size

	ctx := c.Request.Context()

	var total int
	_ = h.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM signature_tasks
		 WHERE assigned_user_id=$1 AND status='open'
	`, claims.UserID).Scan(&total)

	// Cast timestamptz/date/numeric to text in SQL so they scan cleanly into
	// *string for JSON. (Text→text on the read path is a valid pgx decode; the
	// reverse int→text on the write path is not — see the audit-log fix.)
	rows, err := h.pool.Query(ctx, `
		SELECT st.id, st.document_id, st.sequence_no, st.condition_type, st.status,
		       st.opened_at::text, d.doc_format_code, d.doc_no, d.revision,
		       d.doc_date::text, d.amount::text
		  FROM signature_tasks st
		  JOIN documents d ON d.id = st.document_id
		 WHERE st.assigned_user_id=$1 AND st.status='open'
		 ORDER BY st.sequence_no, st.opened_at
		 LIMIT $2 OFFSET $3
	`, claims.UserID, size, offset)
	if err != nil {
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "inbox fetch failed")
		return
	}
	defer rows.Close()

	type taskRow struct {
		ID            int64    `json:"id"`
		DocumentID    int64    `json:"document_id"`
		SequenceNo    int      `json:"sequence_no"`
		ConditionType int16    `json:"condition_type"`
		Status        string   `json:"status"`
		OpenedAt      *string  `json:"opened_at"`
		DocFormatCode string   `json:"doc_format_code"`
		DocNo         string   `json:"doc_no"`
		Revision      int      `json:"revision"`
		DocDate       *string  `json:"doc_date"`
		Amount        *string  `json:"amount"`
	}
	var tasks []taskRow
	for rows.Next() {
		var t taskRow
		if err := rows.Scan(
			&t.ID, &t.DocumentID, &t.SequenceNo, &t.ConditionType, &t.Status,
			&t.OpenedAt, &t.DocFormatCode, &t.DocNo, &t.Revision, &t.DocDate, &t.Amount,
		); err != nil {
			httpx.Error(c, http.StatusInternalServerError, "internal_error", "inbox scan failed")
			return
		}
		tasks = append(tasks, t)
	}
	if tasks == nil {
		tasks = []taskRow{}
	}

	httpx.List(c, http.StatusOK, tasks, httpx.Meta{Total: total, Page: page, Size: size})
}

// GetTask godoc: GET /signature-tasks/:id
func (h *TaskHandler) GetTask(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	taskID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_id", "task id must be an integer")
		return
	}
	ctx := c.Request.Context()

	type taskDetail struct {
		ID             int64   `json:"id"`
		DocumentID     int64   `json:"document_id"`
		SequenceNo     int     `json:"sequence_no"`
		ConditionType  int16   `json:"condition_type"`
		Status         string  `json:"status"`
		AssignedUserID *int64  `json:"assigned_user_id"`
		DocFormatCode  string  `json:"doc_format_code"`
		DocNo          string  `json:"doc_no"`
		Revision       int     `json:"revision"`
		DocStatus      string  `json:"doc_status"`
	}
	var t taskDetail
	err = h.pool.QueryRow(ctx, `
		SELECT st.id, st.document_id, st.sequence_no, st.condition_type, st.status,
		       st.assigned_user_id, d.doc_format_code, d.doc_no, d.revision, d.status
		  FROM signature_tasks st
		  JOIN documents d ON d.id = st.document_id
		 WHERE st.id=$1
	`, taskID).Scan(
		&t.ID, &t.DocumentID, &t.SequenceNo, &t.ConditionType, &t.Status,
		&t.AssignedUserID, &t.DocFormatCode, &t.DocNo, &t.Revision, &t.DocStatus,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(c, http.StatusNotFound, "not_found", "task not found")
		return
	}
	if err != nil {
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "task fetch failed")
		return
	}

	// Only assignee (or admin) can see the task detail.
	if t.AssignedUserID != nil && *t.AssignedUserID != claims.UserID && !hasRole(claims, "system_admin", "document_admin", "auditor") {
		httpx.Error(c, http.StatusForbidden, "forbidden", "not assigned to this task")
		return
	}

	httpx.OK(c, http.StatusOK, t)
}

// Sign godoc: POST /signature-tasks/:id/sign
func (h *TaskHandler) Sign(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	taskID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_id", "task id must be an integer")
		return
	}

	var req struct {
		SignatureImage string `json:"signature_image" binding:"required"` // base64 PNG (or data URL)
		Comment        string `json:"comment"`
		ConsentText    string `json:"consent_text"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_request", "signature_image is required")
		return
	}
	if strings.TrimSpace(req.SignatureImage) == "" {
		httpx.Error(c, http.StatusBadRequest, "signature_required", "signature is required")
		return
	}

	requestID := c.GetString(middleware.RequestIDKey)
	ctx := c.Request.Context()

	// Resolve the document up front so the signature image lands under a stable key.
	var sigDocID int64
	if err := h.pool.QueryRow(ctx, `SELECT document_id FROM signature_tasks WHERE id=$1`, taskID).Scan(&sigDocID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			httpx.Error(c, http.StatusNotFound, "not_found", "task not found")
			return
		}
		h.log.Error("sign: lookup document", zap.Error(err), zap.Int64("task_id", taskID))
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "sign failed")
		return
	}

	// Upload the signature PNG to object storage; engine links it in-tx.
	objectKey, sigSize, sigHash, errCode := decodeAndStoreSignature(ctx, h.store, sigDocID, taskID, req.SignatureImage)
	if errCode != "" {
		status := http.StatusBadRequest
		if errCode == "storage_error" {
			status = http.StatusInternalServerError
		}
		httpx.Error(c, status, errCode, "signature image could not be processed")
		return
	}

	err = h.engine.Sign(ctx, workflow.SignInput{
		TaskID:             taskID,
		SignerUserID:       claims.UserID,
		SignatureImageHash: sigHash,
		SignatureObjectKey: objectKey,
		SignatureSizeBytes: sigSize,
		Comment:            req.Comment,
		ConsentText:        req.ConsentText,
		IPAddress:          c.ClientIP(),
		UserAgent:          c.GetHeader("User-Agent"),
		SessionID:          c.GetHeader("X-Session-ID"),
		RequestID:          requestID,
	})
	if err != nil {
		var alreadyActioned workflow.ErrStepAlreadyActioned
		if errors.As(err, &alreadyActioned) {
			httpx.OK(c, http.StatusOK, gin.H{"message": err.Error(), "already_actioned": true})
			return
		}
		if strings.Contains(err.Error(), "not open") || strings.Contains(err.Error(), "not pending") {
			httpx.Error(c, http.StatusConflict, "document_already_completed", err.Error())
			return
		}
		if strings.Contains(err.Error(), "not assigned") {
			httpx.Error(c, http.StatusForbidden, "not_allowed_to_sign", err.Error())
			return
		}
		h.log.Error("sign task", zap.Error(err), zap.Int64("task_id", taskID))
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "sign failed")
		return
	}

	// If the document is now completed, generate the evidence PDF inline
	// (Phase 1 — no async worker yet; boundary is preserved for Phase 2 River job).
	var docID int64
	var docStatus string
	_ = h.pool.QueryRow(ctx,
		`SELECT document_id, (SELECT status FROM documents WHERE id=document_id) FROM signature_tasks WHERE id=$1`,
		taskID,
	).Scan(&docID, &docStatus)

	if docStatus == "completed" {
		if _, ferr := pdf.FinalizeDocument(ctx, h.pool, h.store, docID); ferr != nil {
			// Non-fatal: document is usable even if evidence PDF generation fails.
			// Log and surface in next reconciliation run.
			h.log.Error("finalize PDF", zap.Error(ferr), zap.Int64("doc_id", docID))
		}
	}

	httpx.OK(c, http.StatusOK, gin.H{"message": "signed", "task_id": taskID})
}

// Reject godoc: POST /signature-tasks/:id/reject
func (h *TaskHandler) Reject(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	taskID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_id", "task id must be an integer")
		return
	}

	var req struct {
		Reason string `json:"reason" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Reason) == "" {
		httpx.Error(c, http.StatusBadRequest, "reason_required", "reject reason is required")
		return
	}

	requestID := c.GetString(middleware.RequestIDKey)

	err = h.engine.Reject(c.Request.Context(), workflow.RejectInput{
		TaskID:       taskID,
		SignerUserID: claims.UserID,
		Reason:       req.Reason,
		IPAddress:    c.ClientIP(),
		UserAgent:    c.GetHeader("User-Agent"),
		RequestID:    requestID,
	})
	if err != nil {
		if strings.Contains(err.Error(), "not assigned") {
			httpx.Error(c, http.StatusForbidden, "not_allowed_to_sign", err.Error())
			return
		}
		if strings.Contains(err.Error(), "not open") {
			httpx.Error(c, http.StatusConflict, "document_already_completed", err.Error())
			return
		}
		h.log.Error("reject task", zap.Error(err), zap.Int64("task_id", taskID))
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "reject failed")
		return
	}

	httpx.OK(c, http.StatusOK, gin.H{"message": "rejected", "task_id": taskID})
}

func hasRole(claims *auth.Claims, roles ...string) bool {
	if claims == nil {
		return false
	}
	for _, r := range claims.Roles {
		for _, want := range roles {
			if r == want {
				return true
			}
		}
	}
	return false
}
