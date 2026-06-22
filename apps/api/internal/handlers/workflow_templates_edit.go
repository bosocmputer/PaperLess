package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"

	"paperless-api/internal/httpx"
	"paperless-api/internal/middleware"
)

// ── Create ──────────────────────────────────────────────────────────────────

type createTemplateBody struct {
	DocFormatCode string `json:"doc_format_code"`
	Name          string `json:"name"`
}

// Create godoc: POST /workflow-templates
// Requires: workflow_admin or system_admin.
// Creates an empty DRAFT template with the next version for its doc_format_code.
// Steps are added separately via PUT /:id/steps.
func (h *WorkflowTemplateHandler) Create(c *gin.Context) {
	var body createTemplateBody
	if err := c.ShouldBindJSON(&body); err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_request", "invalid JSON body")
		return
	}
	docFormatCode := strings.ToUpper(strings.TrimSpace(body.DocFormatCode))
	name := strings.TrimSpace(body.Name)
	if docFormatCode == "" || len(docFormatCode) > 50 {
		httpx.Error(c, http.StatusBadRequest, "invalid_request", "doc_format_code is required (≤50 chars)")
		return
	}
	if name == "" || len(name) > 200 {
		httpx.Error(c, http.StatusBadRequest, "invalid_request", "name is required (≤200 chars)")
		return
	}

	claims := middleware.ClaimsFrom(c)
	ctx := c.Request.Context()

	tx, err := h.pool.Begin(ctx)
	if err != nil {
		h.log.Error("create template: begin tx", zap.Error(err))
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "create failed")
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// Next version for this doc_format_code (mirrors Clone).
	var maxVersion int
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(MAX(version), 0) FROM workflow_templates WHERE doc_format_code=$1
	`, docFormatCode).Scan(&maxVersion); err != nil {
		h.log.Error("create template: max version", zap.Error(err))
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "create failed")
		return
	}
	newVersion := maxVersion + 1

	var newID int64
	err = tx.QueryRow(ctx, `
		INSERT INTO workflow_templates (doc_format_code, name, version, status, created_by)
		VALUES ($1, $2, $3, 'draft', $4)
		RETURNING id
	`, docFormatCode, name, newVersion, claims.UserID).Scan(&newID)
	if isDuplicateKeyHandler(err) {
		httpx.Error(c, http.StatusConflict, "version_conflict", "concurrent create in progress, retry")
		return
	}
	if err != nil {
		h.log.Error("create template: insert", zap.Error(err))
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "create failed")
		return
	}

	_, _ = tx.Exec(ctx, `
		INSERT INTO audit_logs (actor_type, actor_id, action, entity_type, entity_id)
		VALUES ('user', $1, 'workflow_template_created', 'config', $2)
	`, strconv.FormatInt(claims.UserID, 10), strconv.FormatInt(newID, 10))

	if err := tx.Commit(ctx); err != nil {
		h.log.Error("create template: commit", zap.Error(err))
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "create failed")
		return
	}

	httpx.OK(c, http.StatusCreated, gin.H{
		"id":              strconv.FormatInt(newID, 10),
		"doc_format_code": docFormatCode,
		"name":            name,
		"version":         newVersion,
		"status":          "draft",
	})
}

// ── Update (rename) ───────────────────────────────────────────────────────────

type updateTemplateBody struct {
	Name string `json:"name"`
}

// Update godoc: PUT /workflow-templates/:id
// Renames a DRAFT template. doc_format_code/version are identity and immutable.
func (h *WorkflowTemplateHandler) Update(c *gin.Context) {
	tmplID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_id", "template id must be an integer")
		return
	}
	var body updateTemplateBody
	if err := c.ShouldBindJSON(&body); err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_request", "invalid JSON body")
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" || len(name) > 200 {
		httpx.Error(c, http.StatusBadRequest, "invalid_request", "name is required (≤200 chars)")
		return
	}

	claims := middleware.ClaimsFrom(c)
	ctx := c.Request.Context()

	tx, err := h.pool.Begin(ctx)
	if err != nil {
		h.log.Error("update template: begin tx", zap.Error(err))
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "update failed")
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var status string
	err = tx.QueryRow(ctx, `SELECT status FROM workflow_templates WHERE id=$1 FOR UPDATE`, tmplID).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(c, http.StatusNotFound, "not_found", "workflow template not found")
		return
	}
	if err != nil {
		h.log.Error("update template: find", zap.Error(err))
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "update failed")
		return
	}
	if status != "draft" {
		httpx.Error(c, http.StatusConflict, "not_draft", "only a draft template can be edited")
		return
	}

	if _, err := tx.Exec(ctx, `UPDATE workflow_templates SET name=$1 WHERE id=$2`, name, tmplID); err != nil {
		h.log.Error("update template: update", zap.Error(err))
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "update failed")
		return
	}

	_, _ = tx.Exec(ctx, `
		INSERT INTO audit_logs (actor_type, actor_id, action, entity_type, entity_id)
		VALUES ('user', $1, 'workflow_template_updated', 'config', $2)
	`, strconv.FormatInt(claims.UserID, 10), strconv.FormatInt(tmplID, 10))

	if err := tx.Commit(ctx); err != nil {
		h.log.Error("update template: commit", zap.Error(err))
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "update failed")
		return
	}

	httpx.OK(c, http.StatusOK, gin.H{"id": strconv.FormatInt(tmplID, 10), "name": name})
}

// ── UpdateSteps (replace-all) ──────────────────────────────────────────────────

// sigSlot is a signature box position, normalized to the page (0..1) so it is
// resolution-independent. page is 1-based. nil = no on-PDF placement for the step.
type sigSlot struct {
	Page int     `json:"page"`
	X    float64 `json:"x"`
	Y    float64 `json:"y"`
	W    float64 `json:"w"`
	H    float64 `json:"h"`
}

func (s sigSlot) valid() bool {
	const eps = 0.001
	return s.Page >= 1 &&
		s.X >= 0 && s.Y >= 0 && s.W > 0 && s.H > 0 &&
		s.X+s.W <= 1+eps && s.Y+s.H <= 1+eps
}

type stepInput struct {
	PositionCode    string   `json:"position_code"`
	PositionName    string   `json:"position_name"`
	SequenceNo      int      `json:"sequence_no"`
	ConditionType   int      `json:"condition_type"`
	AssigneeUserIDs []int64  `json:"assignee_user_ids"`
	SignatureSlot   *sigSlot `json:"signature_slot,omitempty"`
}

type updateStepsBody struct {
	Steps []stepInput `json:"steps"`
}

// UpdateSteps godoc: PUT /workflow-templates/:id/steps
// Replaces the entire step set (+ assignees) of a DRAFT template in one tx.
// All validation runs before any write so bad input is a 4xx, never a 500.
func (h *WorkflowTemplateHandler) UpdateSteps(c *gin.Context) {
	tmplID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_id", "template id must be an integer")
		return
	}
	var body updateStepsBody
	if err := c.ShouldBindJSON(&body); err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_request", "invalid JSON body")
		return
	}
	if len(body.Steps) == 0 {
		httpx.Error(c, http.StatusBadRequest, "invalid_request", "at least one step is required")
		return
	}

	// ── Stateless validation (no DB) ──────────────────────────────────────────
	seqSeen := map[int]bool{}
	posSeen := map[string]bool{}
	assigneeSet := map[int64]bool{} // union of all assignee ids to validate in one query
	for i := range body.Steps {
		s := &body.Steps[i]
		s.PositionCode = strings.TrimSpace(s.PositionCode)
		s.PositionName = strings.TrimSpace(s.PositionName)
		if s.PositionCode == "" || len(s.PositionCode) > 50 {
			httpx.Error(c, http.StatusBadRequest, "invalid_request", "each step needs a position_code (≤50 chars)")
			return
		}
		if s.PositionName == "" || len(s.PositionName) > 200 {
			httpx.Error(c, http.StatusBadRequest, "invalid_request", "each step needs a position_name (≤200 chars)")
			return
		}
		if s.SequenceNo < 1 {
			httpx.Error(c, http.StatusBadRequest, "invalid_request", "sequence_no must be ≥1")
			return
		}
		if seqSeen[s.SequenceNo] {
			httpx.Error(c, http.StatusBadRequest, "invalid_request", "sequence_no must be unique across steps")
			return
		}
		seqSeen[s.SequenceNo] = true
		if posSeen[s.PositionCode] {
			httpx.Error(c, http.StatusBadRequest, "invalid_request", "position_code must be unique within the template")
			return
		}
		posSeen[s.PositionCode] = true
		if s.ConditionType < 1 || s.ConditionType > 3 {
			httpx.Error(c, http.StatusBadRequest, "invalid_request", "condition_type must be 1, 2, or 3")
			return
		}
		if s.SignatureSlot != nil && !s.SignatureSlot.valid() {
			httpx.Error(c, http.StatusBadRequest, "invalid_signature_slot",
				"signature position must be within the page (normalized 0–1)")
			return
		}
		if s.ConditionType == 3 {
			if len(s.AssigneeUserIDs) > 0 {
				httpx.Error(c, http.StatusBadRequest, "external_step_has_assignees",
					"external (condition 3) steps must not have assignees")
				return
			}
		} else {
			if len(s.AssigneeUserIDs) == 0 {
				httpx.Error(c, http.StatusBadRequest, "invalid_request",
					"condition 1/2 steps require at least one assignee")
				return
			}
			for _, uid := range s.AssigneeUserIDs {
				assigneeSet[uid] = true
			}
		}
	}

	claims := middleware.ClaimsFrom(c)
	ctx := c.Request.Context()

	tx, err := h.pool.Begin(ctx)
	if err != nil {
		h.log.Error("update steps: begin tx", zap.Error(err))
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "update failed")
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// Lock template + assert draft.
	var status string
	err = tx.QueryRow(ctx, `SELECT status FROM workflow_templates WHERE id=$1 FOR UPDATE`, tmplID).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(c, http.StatusNotFound, "not_found", "workflow template not found")
		return
	}
	if err != nil {
		h.log.Error("update steps: find", zap.Error(err))
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "update failed")
		return
	}
	if status != "draft" {
		httpx.Error(c, http.StatusConflict, "not_draft", "only a draft template can be edited")
		return
	}

	// Validate that every referenced assignee exists and is active.
	if len(assigneeSet) > 0 {
		ids := make([]int64, 0, len(assigneeSet))
		for id := range assigneeSet {
			ids = append(ids, id)
		}
		var validCount int
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FROM users WHERE id = ANY($1) AND status='active'
		`, ids).Scan(&validCount); err != nil {
			h.log.Error("update steps: validate assignees", zap.Error(err))
			httpx.Error(c, http.StatusInternalServerError, "internal_error", "update failed")
			return
		}
		if validCount != len(ids) {
			httpx.Error(c, http.StatusBadRequest, "invalid_assignee",
				"one or more assignees do not exist or are inactive")
			return
		}
	}

	// Replace-all: delete existing steps (cascade removes assignees), then insert.
	if _, err := tx.Exec(ctx, `DELETE FROM workflow_steps WHERE workflow_template_id=$1`, tmplID); err != nil {
		h.log.Error("update steps: delete old", zap.Error(err))
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "update failed")
		return
	}

	for i := range body.Steps {
		s := &body.Steps[i]
		var slotJSON []byte
		if s.SignatureSlot != nil {
			slotJSON, _ = json.Marshal(s.SignatureSlot) // validated above
		}
		var stepID int64
		err = tx.QueryRow(ctx, `
			INSERT INTO workflow_steps
			       (workflow_template_id, position_code, position_name, sequence_no, condition_type, signature_slot)
			VALUES ($1, $2, $3, $4, $5, $6)
			RETURNING id
		`, tmplID, s.PositionCode, s.PositionName, s.SequenceNo, s.ConditionType, slotJSON).Scan(&stepID)
		if err != nil {
			h.log.Error("update steps: insert step", zap.Error(err))
			httpx.Error(c, http.StatusInternalServerError, "internal_error", "update failed")
			return
		}
		for order, uid := range s.AssigneeUserIDs {
			if _, err := tx.Exec(ctx, `
				INSERT INTO workflow_step_assignees (workflow_step_id, user_id, display_order)
				VALUES ($1, $2, $3)
			`, stepID, uid, order); err != nil {
				h.log.Error("update steps: insert assignee", zap.Error(err))
				httpx.Error(c, http.StatusInternalServerError, "internal_error", "update failed")
				return
			}
		}
	}

	_, _ = tx.Exec(ctx, `
		INSERT INTO audit_logs (actor_type, actor_id, action, entity_type, entity_id)
		VALUES ('user', $1, 'workflow_template_steps_updated', 'config', $2)
	`, strconv.FormatInt(claims.UserID, 10), strconv.FormatInt(tmplID, 10))

	if err := tx.Commit(ctx); err != nil {
		h.log.Error("update steps: commit", zap.Error(err))
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "update failed")
		return
	}

	httpx.OK(c, http.StatusOK, gin.H{
		"id":         strconv.FormatInt(tmplID, 10),
		"step_count": len(body.Steps),
	})
}
