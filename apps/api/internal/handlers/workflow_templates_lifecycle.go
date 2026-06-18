package handlers

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/zap"

	"paperless-api/internal/httpx"
	"paperless-api/internal/middleware"
)

// isDuplicateKeyHandler returns true when err is a PostgreSQL unique-violation (SQLSTATE 23505).
// Defined here because the workflow.isDuplicateKey is unexported (workflow package).
func isDuplicateKeyHandler(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// Clone godoc: POST /workflow-templates/:id/clone
// Copies a template (any status) + its steps + assignees into a new draft with
// the next version number for the same doc_format_code.
// Returns the new template id.
func (h *WorkflowTemplateHandler) Clone(c *gin.Context) {
	srcID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_id", "template id must be an integer")
		return
	}

	claims := middleware.ClaimsFrom(c)
	ctx := c.Request.Context()

	tx, err := h.pool.Begin(ctx)
	if err != nil {
		h.log.Error("clone: begin tx", zap.Error(err))
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "clone failed")
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// Lock and read source template.
	var (
		docFormatCode string
		name          string
	)
	err = tx.QueryRow(ctx, `
		SELECT doc_format_code, name
		  FROM workflow_templates
		 WHERE id=$1
		   FOR UPDATE
	`, srcID).Scan(&docFormatCode, &name)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(c, http.StatusNotFound, "not_found", "workflow template not found")
		return
	}
	if err != nil {
		h.log.Error("clone: find source", zap.Error(err))
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "clone failed")
		return
	}

	// Determine next version for this doc_format_code.
	var maxVersion int
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(MAX(version), 0)
		  FROM workflow_templates
		 WHERE doc_format_code=$1
	`, docFormatCode).Scan(&maxVersion); err != nil {
		h.log.Error("clone: max version", zap.Error(err))
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "clone failed")
		return
	}
	newVersion := maxVersion + 1

	// Insert new draft template.
	var newTmplID int64
	err = tx.QueryRow(ctx, `
		INSERT INTO workflow_templates (doc_format_code, name, version, status, created_by)
		VALUES ($1, $2, $3, 'draft', $4)
		RETURNING id
	`, docFormatCode, name, newVersion, claims.UserID).Scan(&newTmplID)
	if isDuplicateKeyHandler(err) {
		// Race on (doc_format_code, version) — extremely rare; safe 409.
		httpx.Error(c, http.StatusConflict, "version_conflict", "concurrent clone in progress, retry")
		return
	}
	if err != nil {
		h.log.Error("clone: insert template", zap.Error(err))
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "clone failed")
		return
	}

	// Read steps + assignees in a single JOIN. All rows are materialized into
	// memory before any writes, avoiding pgx "conn busy" (open rows block
	// further queries on the same connection).
	type srcAssignee struct {
		userID       int64
		displayOrder *int16
	}
	type srcStep struct {
		id            int64
		posCode       string
		posName       string
		seqNo         int
		condType      int
		signatureSlot []byte
		assignees     []srcAssignee
	}

	saRows, err := tx.Query(ctx, `
		SELECT ws.id, ws.position_code, ws.position_name, ws.sequence_no,
		       ws.condition_type, ws.signature_slot,
		       wsa.user_id, wsa.display_order
		  FROM workflow_steps ws
		  LEFT JOIN workflow_step_assignees wsa ON wsa.workflow_step_id = ws.id
		 WHERE ws.workflow_template_id=$1
		 ORDER BY ws.sequence_no, wsa.display_order, wsa.id
	`, srcID)
	if err != nil {
		h.log.Error("clone: read steps+assignees", zap.Error(err))
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "clone failed")
		return
	}

	stepMap := map[int64]*srcStep{}
	var stepOrder []int64
	for saRows.Next() {
		var (
			stepID       int64
			posCode      string
			posName      string
			seqNo        int
			condType     int
			slot         []byte
			aUserID      *int64
			aDisplayOrder *int16
		)
		if err := saRows.Scan(&stepID, &posCode, &posName, &seqNo, &condType, &slot, &aUserID, &aDisplayOrder); err != nil {
			saRows.Close()
			h.log.Error("clone: scan step+assignee row", zap.Error(err))
			httpx.Error(c, http.StatusInternalServerError, "internal_error", "clone failed")
			return
		}
		if _, exists := stepMap[stepID]; !exists {
			stepMap[stepID] = &srcStep{
				id: stepID, posCode: posCode, posName: posName,
				seqNo: seqNo, condType: condType, signatureSlot: slot,
			}
			stepOrder = append(stepOrder, stepID)
		}
		if aUserID != nil {
			stepMap[stepID].assignees = append(stepMap[stepID].assignees, srcAssignee{
				userID: *aUserID, displayOrder: aDisplayOrder,
			})
		}
	}
	if err := saRows.Err(); err != nil {
		h.log.Error("clone: steps rows err", zap.Error(err))
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "clone failed")
		return
	}
	saRows.Close() // explicit close before any writes

	// Insert cloned steps and their assignees — rows closed above, no conn busy.
	for _, sid := range stepOrder {
		s := stepMap[sid]
		var newStepID int64
		err = tx.QueryRow(ctx, `
			INSERT INTO workflow_steps
			       (workflow_template_id, position_code, position_name, sequence_no, condition_type, signature_slot)
			VALUES ($1, $2, $3, $4, $5, $6)
			RETURNING id
		`, newTmplID, s.posCode, s.posName, s.seqNo, s.condType, s.signatureSlot).Scan(&newStepID)
		if err != nil {
			h.log.Error("clone: insert step", zap.Error(err))
			httpx.Error(c, http.StatusInternalServerError, "internal_error", "clone failed")
			return
		}
		for _, a := range s.assignees {
			if _, err := tx.Exec(ctx, `
				INSERT INTO workflow_step_assignees (workflow_step_id, user_id, display_order)
				VALUES ($1, $2, $3)
			`, newStepID, a.userID, a.displayOrder); err != nil {
				h.log.Error("clone: insert assignee", zap.Error(err))
				httpx.Error(c, http.StatusInternalServerError, "internal_error", "clone failed")
				return
			}
		}
	}

	// Audit the clone.
	_, _ = tx.Exec(ctx, `
		INSERT INTO audit_logs (actor_type, actor_id, action, entity_type, entity_id)
		VALUES ('user', $1, 'workflow_template_cloned', 'config', $2)
	`, strconv.FormatInt(claims.UserID, 10), strconv.FormatInt(newTmplID, 10))

	if err := tx.Commit(ctx); err != nil {
		h.log.Error("clone: commit", zap.Error(err))
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "clone failed")
		return
	}

	httpx.OK(c, http.StatusCreated, gin.H{
		"id":             strconv.FormatInt(newTmplID, 10),
		"doc_format_code": docFormatCode,
		"name":           name,
		"version":        newVersion,
		"status":         "draft",
	})
}

// Publish godoc: POST /workflow-templates/:id/publish
// Flips a draft template to active.
// Atomically demotes the current active (if any) for the same doc_format_code to inactive
// in the same tx, so at most one active per format is guaranteed.
// Under concurrent publishes the partial unique index uq_workflow_active_per_format
// allows only one winner — the loser gets 23505 which maps to 409 conflict_active_exists.
func (h *WorkflowTemplateHandler) Publish(c *gin.Context) {
	tmplID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_id", "template id must be an integer")
		return
	}

	claims := middleware.ClaimsFrom(c)
	ctx := c.Request.Context()

	tx, err := h.pool.Begin(ctx)
	if err != nil {
		h.log.Error("publish: begin tx", zap.Error(err))
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "publish failed")
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// Lock the target template.
	var (
		docFormatCode string
		currentStatus string
	)
	err = tx.QueryRow(ctx, `
		SELECT doc_format_code, status
		  FROM workflow_templates
		 WHERE id=$1
		   FOR UPDATE
	`, tmplID).Scan(&docFormatCode, &currentStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(c, http.StatusNotFound, "not_found", "workflow template not found")
		return
	}
	if err != nil {
		h.log.Error("publish: find template", zap.Error(err))
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "publish failed")
		return
	}

	if currentStatus == "active" {
		// Already active — idempotent.
		tx.Rollback(ctx) //nolint:errcheck
		httpx.OK(c, http.StatusOK, gin.H{"id": strconv.FormatInt(tmplID, 10), "status": "active"})
		return
	}
	if currentStatus != "draft" {
		httpx.Error(c, http.StatusConflict, "template_not_publishable",
			"only a draft template can be published")
		return
	}

	// Demote any existing active for this doc_format_code to inactive.
	// This runs BEFORE the target is set to active so the partial unique index
	// (only one active per format) is satisfied within the same tx.
	if _, err := tx.Exec(ctx, `
		UPDATE workflow_templates
		   SET status='inactive'
		 WHERE doc_format_code=$1 AND status='active' AND id<>$2
	`, docFormatCode, tmplID); err != nil {
		h.log.Error("publish: demote active", zap.Error(err))
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "publish failed")
		return
	}

	// Activate the target.
	if _, err := tx.Exec(ctx, `
		UPDATE workflow_templates
		   SET status='active', effective_from=now()
		 WHERE id=$1
	`, tmplID); err != nil {
		if isDuplicateKeyHandler(err) {
			// A concurrent publish for the same format won the race.
			httpx.Error(c, http.StatusConflict, "conflict_active_exists",
				"another version was published concurrently")
			return
		}
		h.log.Error("publish: activate", zap.Error(err))
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "publish failed")
		return
	}

	// Audit.
	_, _ = tx.Exec(ctx, `
		INSERT INTO audit_logs (actor_type, actor_id, action, entity_type, entity_id)
		VALUES ('user', $1, 'workflow_template_published', 'config', $2)
	`, strconv.FormatInt(claims.UserID, 10), strconv.FormatInt(tmplID, 10))

	if err := tx.Commit(ctx); err != nil {
		if isDuplicateKeyHandler(err) {
			// Unique-index violation surfaced at commit time (deferred constraint path).
			httpx.Error(c, http.StatusConflict, "conflict_active_exists",
				"another version was published concurrently")
			return
		}
		h.log.Error("publish: commit", zap.Error(err))
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "publish failed")
		return
	}

	httpx.OK(c, http.StatusOK, gin.H{
		"id":     strconv.FormatInt(tmplID, 10),
		"status": "active",
	})
}

// Deactivate godoc: POST /workflow-templates/:id/deactivate
// Flips an active template to inactive.
// In-flight documents are unaffected — they bind workflow_version at import time.
func (h *WorkflowTemplateHandler) Deactivate(c *gin.Context) {
	tmplID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_id", "template id must be an integer")
		return
	}

	claims := middleware.ClaimsFrom(c)
	ctx := c.Request.Context()

	tx, err := h.pool.Begin(ctx)
	if err != nil {
		h.log.Error("deactivate: begin tx", zap.Error(err))
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "deactivate failed")
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var currentStatus string
	err = tx.QueryRow(ctx, `
		SELECT status FROM workflow_templates WHERE id=$1 FOR UPDATE
	`, tmplID).Scan(&currentStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(c, http.StatusNotFound, "not_found", "workflow template not found")
		return
	}
	if err != nil {
		h.log.Error("deactivate: find template", zap.Error(err))
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "deactivate failed")
		return
	}

	if currentStatus == "inactive" {
		// Already inactive — idempotent.
		tx.Rollback(ctx) //nolint:errcheck
		httpx.OK(c, http.StatusOK, gin.H{"id": strconv.FormatInt(tmplID, 10), "status": "inactive"})
		return
	}
	if currentStatus != "active" {
		httpx.Error(c, http.StatusConflict, "template_not_deactivatable",
			"only an active template can be deactivated")
		return
	}

	if _, err := tx.Exec(ctx, `
		UPDATE workflow_templates SET status='inactive' WHERE id=$1
	`, tmplID); err != nil {
		h.log.Error("deactivate: update", zap.Error(err))
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "deactivate failed")
		return
	}

	// Audit.
	_, _ = tx.Exec(ctx, `
		INSERT INTO audit_logs (actor_type, actor_id, action, entity_type, entity_id)
		VALUES ('user', $1, 'workflow_template_deactivated', 'config', $2)
	`, strconv.FormatInt(claims.UserID, 10), strconv.FormatInt(tmplID, 10))

	if err := tx.Commit(ctx); err != nil {
		h.log.Error("deactivate: commit", zap.Error(err))
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "deactivate failed")
		return
	}

	httpx.OK(c, http.StatusOK, gin.H{
		"id":     strconv.FormatInt(tmplID, 10),
		"status": "inactive",
	})
}
