package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"paperless-api/internal/httpx"
)

type WorkflowTemplateHandler struct {
	pool *pgxpool.Pool
	log  *zap.Logger
}

func NewWorkflowTemplateHandler(pool *pgxpool.Pool, log *zap.Logger) *WorkflowTemplateHandler {
	return &WorkflowTemplateHandler{pool: pool, log: log}
}

// ListTemplates godoc: GET /workflow-templates
// Requires: workflow_admin or system_admin.
// Optional query param: doc_format_code. Bounded at 200 rows.
func (h *WorkflowTemplateHandler) ListTemplates(c *gin.Context) {
	ctx := c.Request.Context()

	docFormatCode := c.Query("doc_format_code")

	var (
		where []string
		args  []any
		argN  = 1
	)
	if docFormatCode != "" {
		where = append(where, "doc_format_code = $"+strconv.Itoa(argN))
		args = append(args, docFormatCode)
		argN++
	}

	whereClause := ""
	if len(where) > 0 {
		whereClause = " WHERE " + where[0]
	}

	// cap at 200; always ordered by doc_format_code, version for stable paging
	query := `
		SELECT id, doc_format_code, name, version, status,
		       COALESCE(effective_from::text, '') AS effective_from,
		       created_at::text
		  FROM workflow_templates` + whereClause + `
		 ORDER BY doc_format_code, version
		 LIMIT 200`

	rows, err := h.pool.Query(ctx, query, args...)
	if err != nil {
		h.log.Error("list workflow templates", zap.Error(err))
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "list failed")
		return
	}
	defer rows.Close()

	type templateRow struct {
		ID            string `json:"id"`
		DocFormatCode string `json:"doc_format_code"`
		Name          string `json:"name"`
		Version       int    `json:"version"`
		Status        string `json:"status"`
		EffectiveFrom string `json:"effective_from,omitempty"`
		CreatedAt     string `json:"created_at"`
	}

	var templates []templateRow
	for rows.Next() {
		var t templateRow
		var id int64
		if err := rows.Scan(&id, &t.DocFormatCode, &t.Name, &t.Version, &t.Status,
			&t.EffectiveFrom, &t.CreatedAt); err != nil {
			h.log.Error("scan template row", zap.Error(err))
			httpx.Error(c, http.StatusInternalServerError, "internal_error", "list failed")
			return
		}
		t.ID = strconv.FormatInt(id, 10)
		templates = append(templates, t)
	}
	if err := rows.Err(); err != nil {
		h.log.Error("rows error", zap.Error(err))
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "list failed")
		return
	}
	if templates == nil {
		templates = []templateRow{}
	}
	httpx.OK(c, http.StatusOK, templates)
}

// tmplAssigneeDetail is the per-assignee payload returned inside a step.
type tmplAssigneeDetail struct {
	UserID       string `json:"user_id"`
	Username     string `json:"username"`
	DisplayName  string `json:"display_name"`
	DisplayOrder *int   `json:"display_order"`
}

// tmplStepDetail is the per-step payload returned inside a template detail.
type tmplStepDetail struct {
	ID            string               `json:"id"`
	SequenceNo    int                  `json:"sequence_no"`
	PositionCode  string               `json:"position_code"`
	PositionName  string               `json:"position_name"`
	ConditionType int                  `json:"condition_type"`
	SignatureSlot json.RawMessage      `json:"signature_slot,omitempty"`
	Assignees     []tmplAssigneeDetail `json:"assignees"`
}

// tmplDetailResponse is the full template detail payload.
type tmplDetailResponse struct {
	ID            string           `json:"id"`
	DocFormatCode string           `json:"doc_format_code"`
	Name          string           `json:"name"`
	Version       int              `json:"version"`
	Status        string           `json:"status"`
	EffectiveFrom string           `json:"effective_from,omitempty"`
	CreatedAt     string           `json:"created_at"`
	Steps         []tmplStepDetail `json:"steps"`
}

// GetTemplate godoc: GET /workflow-templates/:id
// Requires: workflow_admin or system_admin.
// Returns the template + ordered steps + each step's assignees.
// Uses a single JOIN query to avoid N+1 — steps and assignees are
// grouped in Go after the scan.
func (h *WorkflowTemplateHandler) GetTemplate(c *gin.Context) {
	tmplID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_id", "template id must be an integer")
		return
	}

	ctx := c.Request.Context()

	// ── Template header ──────────────────────────────────────────────────────
	var tmpl tmplDetailResponse
	var rawID int64
	err = h.pool.QueryRow(ctx, `
		SELECT id, doc_format_code, name, version, status,
		       COALESCE(effective_from::text, ''), created_at::text
		  FROM workflow_templates WHERE id=$1
	`, tmplID).Scan(&rawID, &tmpl.DocFormatCode, &tmpl.Name, &tmpl.Version,
		&tmpl.Status, &tmpl.EffectiveFrom, &tmpl.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(c, http.StatusNotFound, "not_found", "workflow template not found")
		return
	}
	if err != nil {
		h.log.Error("get template", zap.Error(err))
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "fetch failed")
		return
	}
	tmpl.ID = strconv.FormatInt(rawID, 10)

	// ── Steps + assignees in one query (no N+1) ──────────────────────────────
	// LEFT JOIN so steps with no assignees still appear (condition_type=3
	// external-signer steps have no workflow_step_assignees rows).
	rows, err := h.pool.Query(ctx, `
		SELECT ws.id, ws.sequence_no, ws.position_code, ws.position_name,
		       ws.condition_type, ws.signature_slot,
		       wsa.user_id,    wsa.display_order,
		       u.username,     u.display_name
		  FROM workflow_steps ws
		  LEFT JOIN workflow_step_assignees wsa ON wsa.workflow_step_id = ws.id
		  LEFT JOIN users u ON u.id = wsa.user_id
		 WHERE ws.workflow_template_id = $1
		 ORDER BY ws.sequence_no, wsa.display_order, wsa.id
	`, tmplID)
	if err != nil {
		h.log.Error("get template steps", zap.Error(err))
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "fetch failed")
		return
	}
	defer rows.Close()

	// Group rows into steps map; preserve insertion order via stepOrder slice.
	stepMap := map[int64]*tmplStepDetail{}
	var stepOrder []int64

	for rows.Next() {
		var (
			stepID         int64
			seqNo          int
			posCode        string
			posName        string
			condType       int
			slot           []byte
			assigneeUserID *int64
			displayOrder   *int
			username       *string
			displayName    *string
		)
		if err := rows.Scan(
			&stepID, &seqNo, &posCode, &posName, &condType, &slot,
			&assigneeUserID, &displayOrder,
			&username, &displayName,
		); err != nil {
			h.log.Error("scan step row", zap.Error(err))
			httpx.Error(c, http.StatusInternalServerError, "internal_error", "fetch failed")
			return
		}

		if _, exists := stepMap[stepID]; !exists {
			sd := tmplStepDetail{
				ID:            strconv.FormatInt(stepID, 10),
				SequenceNo:    seqNo,
				PositionCode:  posCode,
				PositionName:  posName,
				ConditionType: condType,
				Assignees:     []tmplAssigneeDetail{},
			}
			if len(slot) > 0 {
				sd.SignatureSlot = json.RawMessage(slot)
			}
			stepMap[stepID] = &sd
			stepOrder = append(stepOrder, stepID)
		}

		if assigneeUserID != nil && username != nil && displayName != nil {
			stepMap[stepID].Assignees = append(stepMap[stepID].Assignees, tmplAssigneeDetail{
				UserID:       strconv.FormatInt(*assigneeUserID, 10),
				Username:     *username,
				DisplayName:  *displayName,
				DisplayOrder: displayOrder,
			})
		}
	}
	if err := rows.Err(); err != nil {
		h.log.Error("steps rows error", zap.Error(err))
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "fetch failed")
		return
	}

	tmpl.Steps = make([]tmplStepDetail, 0, len(stepOrder))
	for _, sid := range stepOrder {
		tmpl.Steps = append(tmpl.Steps, *stepMap[sid])
	}

	httpx.OK(c, http.StatusOK, tmpl)
}
