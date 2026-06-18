package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"paperless-api/internal/middleware"
)

// ── router helper ─────────────────────────────────────────────────────────────

func newTmplRouter(pool *pgxpool.Pool, role string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(gin.Recovery())
	h := NewWorkflowTemplateHandler(pool, zap.NewNop())
	guard := middleware.RequireRole("workflow_admin", "system_admin")
	r.GET("/workflow-templates", fakeAuth(1, role), guard, h.ListTemplates)
	r.GET("/workflow-templates/:id", fakeAuth(1, role), guard, h.GetTemplate)
	return r
}

// ── seed helpers ──────────────────────────────────────────────────────────────

// seedTemplateWithSteps creates a template with two steps (condition 1 and 2)
// and assigns maker to step 1, checkerA+checkerB to step 2.
// Returns the template id. Cleaned up on test end.
func seedTemplateWithSteps(t *testing.T, pool *pgxpool.Pool) int64 {
	t.Helper()
	ctx := context.Background()
	suffix := time.Now().UnixNano()
	fmtCode := fmt.Sprintf("TMPL%d", suffix)

	var tmplID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO workflow_templates (doc_format_code, name, version, status, effective_from, created_by)
		SELECT $1, 'test template', 1, 'draft', now(), u.id
		  FROM users u WHERE u.username='admin'
		RETURNING id
	`, fmtCode).Scan(&tmplID); err != nil {
		t.Fatalf("seed template: %v", err)
	}

	var stepID1, stepID2 int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO workflow_steps (workflow_template_id, position_code, position_name, sequence_no, condition_type)
		VALUES ($1, 'MAKER', 'ผู้จัดทำ', 1, 1) RETURNING id
	`, tmplID).Scan(&stepID1); err != nil {
		t.Fatalf("seed step1: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO workflow_steps (workflow_template_id, position_code, position_name, sequence_no, condition_type)
		VALUES ($1, 'CHECKER', 'ผู้ตรวจสอบ', 2, 2) RETURNING id
	`, tmplID).Scan(&stepID2); err != nil {
		t.Fatalf("seed step2: %v", err)
	}

	// Assign maker to step 1.
	if _, err := pool.Exec(ctx, `
		INSERT INTO workflow_step_assignees (workflow_step_id, user_id, display_order)
		SELECT $1, u.id, 1 FROM users u WHERE u.username='maker'
	`, stepID1); err != nil {
		t.Fatalf("seed assignee maker: %v", err)
	}
	// Assign checkerA (order 1) and checkerB (order 2) to step 2.
	if _, err := pool.Exec(ctx, `
		INSERT INTO workflow_step_assignees (workflow_step_id, user_id, display_order)
		SELECT $1, u.id, 1 FROM users u WHERE u.username='checkerA'
	`, stepID2); err != nil {
		t.Fatalf("seed assignee checkerA: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO workflow_step_assignees (workflow_step_id, user_id, display_order)
		SELECT $1, u.id, 2 FROM users u WHERE u.username='checkerB'
	`, stepID2); err != nil {
		t.Fatalf("seed assignee checkerB: %v", err)
	}

	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM workflow_step_assignees WHERE workflow_step_id IN
			(SELECT id FROM workflow_steps WHERE workflow_template_id=$1)`, tmplID)
		_, _ = pool.Exec(ctx, `DELETE FROM workflow_steps WHERE workflow_template_id=$1`, tmplID)
		_, _ = pool.Exec(ctx, `DELETE FROM workflow_templates WHERE id=$1`, tmplID)
	})
	return tmplID
}

// resolveTemplateID returns the id of the seeded template matching doc_format_code + version.
func resolveTemplateID(t *testing.T, pool *pgxpool.Pool, docFormatCode string, version int) int64 {
	t.Helper()
	var id int64
	if err := pool.QueryRow(context.Background(),
		`SELECT id FROM workflow_templates WHERE doc_format_code=$1 AND version=$2`,
		docFormatCode, version,
	).Scan(&id); err != nil {
		t.Fatalf("resolve template %s v%d: %v", docFormatCode, version, err)
	}
	return id
}

// ── Role guard ────────────────────────────────────────────────────────────────

func TestWorkflowTemplate_RoleGuard(t *testing.T) {
	pool := validationPool(t)
	tmplID := resolveTemplateID(t, pool, "POP", 1)

	deniedRoles := []string{"document_admin", "auditor", "signer"}
	allowedRoles := []string{"workflow_admin", "system_admin"}

	for _, role := range deniedRoles {
		t.Run("list/"+role, func(t *testing.T) {
			r := newTmplRouter(pool, role)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest("GET", "/workflow-templates", nil))
			t.Logf("list %s → %d", role, w.Code)
			if w.Code != http.StatusForbidden {
				t.Errorf("role %s: want 403, got %d", role, w.Code)
			}
		})
		t.Run("get/"+role, func(t *testing.T) {
			r := newTmplRouter(pool, role)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest("GET",
				fmt.Sprintf("/workflow-templates/%d", tmplID), nil))
			t.Logf("get %s → %d", role, w.Code)
			if w.Code != http.StatusForbidden {
				t.Errorf("role %s: want 403, got %d", role, w.Code)
			}
		})
	}

	for _, role := range allowedRoles {
		t.Run("list/"+role, func(t *testing.T) {
			r := newTmplRouter(pool, role)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest("GET", "/workflow-templates", nil))
			t.Logf("list %s → %d", role, w.Code)
			if w.Code != http.StatusOK {
				t.Errorf("role %s: want 200, got %d: %s", role, w.Code, w.Body.String())
			}
		})
	}
}

// ── List ──────────────────────────────────────────────────────────────────────

// TestWorkflowTemplate_List_ReturnsPOPAndDEMO3 verifies the seeded templates
// appear in the list response with the correct fields.
func TestWorkflowTemplate_List_ReturnsPOPAndDEMO3(t *testing.T) {
	pool := validationPool(t)
	r := newTmplRouter(pool, "workflow_admin")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/workflow-templates", nil))
	t.Logf("list all → %d %s", w.Code, w.Body.String())
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}

	var resp struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Data) == 0 {
		t.Fatal("expected at least one template")
	}

	// Build a set of doc_format_codes present.
	formats := map[string]bool{}
	for _, row := range resp.Data {
		if fc, ok := row["doc_format_code"].(string); ok {
			formats[fc] = true
		}
		// Each row must include required fields.
		for _, field := range []string{"id", "doc_format_code", "name", "version", "status", "created_at"} {
			if _, ok := row[field]; !ok {
				t.Errorf("row missing field %q: %v", field, row)
			}
		}
		// id must be a string (FormatInt), not a float64.
		if id, ok := row["id"].(string); !ok || id == "" {
			t.Errorf("id must be a non-empty string, got %T %v", row["id"], row["id"])
		}
	}
	if !formats["POP"] {
		t.Error("expected POP template in list")
	}
	if !formats["DEMO3"] {
		t.Error("expected DEMO3 template in list")
	}
}

// TestWorkflowTemplate_List_DocFormatFilter verifies the doc_format_code filter
// returns only the matching templates.
func TestWorkflowTemplate_List_DocFormatFilter(t *testing.T) {
	pool := validationPool(t)
	r := newTmplRouter(pool, "workflow_admin")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/workflow-templates?doc_format_code=POP", nil))
	t.Logf("filter POP → %d %s", w.Code, w.Body.String())
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}

	var resp struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, row := range resp.Data {
		if fc := row["doc_format_code"].(string); fc != "POP" {
			t.Errorf("filter returned non-POP template: %q", fc)
		}
	}
	if len(resp.Data) == 0 {
		t.Error("expected at least one POP template")
	}

	// Unknown format must return empty list, not 404 or 500.
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/workflow-templates?doc_format_code=DOESNOTEXIST", nil))
	t.Logf("filter unknown → %d %s", w.Code, w.Body.String())
	if w.Code != http.StatusOK {
		t.Errorf("unknown format: want 200, got %d", w.Code)
	}
	var resp2 struct {
		Data []any `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp2)
	if len(resp2.Data) != 0 {
		t.Errorf("expected empty list for unknown format, got %d rows", len(resp2.Data))
	}
}

// TestWorkflowTemplate_List_EmptyIsArray verifies the list returns [] not null
// when no templates match.
func TestWorkflowTemplate_List_EmptyIsArray(t *testing.T) {
	pool := validationPool(t)
	r := newTmplRouter(pool, "workflow_admin")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/workflow-templates?doc_format_code=EMPTYZZZZ", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	// data must be [] not null.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if string(raw["data"]) == "null" {
		t.Error("data must be [] not null when empty")
	}
}

// ── Detail ────────────────────────────────────────────────────────────────────

// TestWorkflowTemplate_Get_POPStepTree verifies the POP template (seeded in 0002)
// returns the correct ordered step/assignee tree:
//   - 3 steps in order (MAKER, CHECKER, APPROVER)
//   - MAKER: condition_type=1, 1 assignee (maker)
//   - CHECKER: condition_type=2, 2 assignees (checkerA, checkerB in order)
//   - APPROVER: condition_type=1, 1 assignee (approver)
func TestWorkflowTemplate_Get_POPStepTree(t *testing.T) {
	pool := validationPool(t)
	tmplID := resolveTemplateID(t, pool, "POP", 1)

	r := newTmplRouter(pool, "workflow_admin")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET",
		fmt.Sprintf("/workflow-templates/%d", tmplID), nil))
	t.Logf("GET POP → %d %s", w.Code, w.Body.String())
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}

	var resp struct {
		Data struct {
			ID            string `json:"id"`
			DocFormatCode string `json:"doc_format_code"`
			Name          string `json:"name"`
			Version       int    `json:"version"`
			Status        string `json:"status"`
			CreatedAt     string `json:"created_at"`
			Steps         []struct {
				ID            string `json:"id"`
				SequenceNo    int    `json:"sequence_no"`
				PositionCode  string `json:"position_code"`
				PositionName  string `json:"position_name"`
				ConditionType int    `json:"condition_type"`
				Assignees     []struct {
					UserID       string `json:"user_id"`
					Username     string `json:"username"`
					DisplayName  string `json:"display_name"`
					DisplayOrder *int   `json:"display_order"`
				} `json:"assignees"`
			} `json:"steps"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	d := resp.Data
	if d.DocFormatCode != "POP" {
		t.Errorf("doc_format_code: want POP, got %q", d.DocFormatCode)
	}
	if d.ID == "" {
		t.Error("id must not be empty")
	}
	if d.CreatedAt == "" {
		t.Error("created_at must not be empty")
	}
	// id must be a string, parseable as int.
	if _, err := strconv.ParseInt(d.ID, 10, 64); err != nil {
		t.Errorf("id must be a parseable int64 string, got %q: %v", d.ID, err)
	}

	if len(d.Steps) != 3 {
		t.Fatalf("want 3 steps, got %d", len(d.Steps))
	}

	// Step ordering must be by sequence_no.
	wantSeq := []int{1, 2, 3}
	wantCodes := []string{"MAKER", "CHECKER", "APPROVER"}
	wantConditions := []int{1, 2, 1}
	for i, s := range d.Steps {
		if s.SequenceNo != wantSeq[i] {
			t.Errorf("step[%d] sequence_no: want %d, got %d", i, wantSeq[i], s.SequenceNo)
		}
		if s.PositionCode != wantCodes[i] {
			t.Errorf("step[%d] position_code: want %q, got %q", i, wantCodes[i], s.PositionCode)
		}
		if s.ConditionType != wantConditions[i] {
			t.Errorf("step[%d] condition_type: want %d, got %d", i, wantConditions[i], s.ConditionType)
		}
		if s.ID == "" {
			t.Errorf("step[%d] id must not be empty", i)
		}
	}

	// MAKER step: exactly 1 assignee = maker.
	makerStep := d.Steps[0]
	if len(makerStep.Assignees) != 1 {
		t.Errorf("MAKER: want 1 assignee, got %d", len(makerStep.Assignees))
	} else if makerStep.Assignees[0].Username != "maker" {
		t.Errorf("MAKER assignee: want maker, got %q", makerStep.Assignees[0].Username)
	}

	// CHECKER step: exactly 2 assignees, checkerA (order 1) then checkerB (order 2).
	checkerStep := d.Steps[1]
	if len(checkerStep.Assignees) != 2 {
		t.Errorf("CHECKER: want 2 assignees, got %d", len(checkerStep.Assignees))
	} else {
		if checkerStep.Assignees[0].Username != "checkerA" {
			t.Errorf("CHECKER[0]: want checkerA, got %q", checkerStep.Assignees[0].Username)
		}
		if checkerStep.Assignees[1].Username != "checkerB" {
			t.Errorf("CHECKER[1]: want checkerB, got %q", checkerStep.Assignees[1].Username)
		}
	}

	// APPROVER step: exactly 1 assignee = approver.
	approverStep := d.Steps[2]
	if len(approverStep.Assignees) != 1 {
		t.Errorf("APPROVER: want 1 assignee, got %d", len(approverStep.Assignees))
	} else if approverStep.Assignees[0].Username != "approver" {
		t.Errorf("APPROVER assignee: want approver, got %q", approverStep.Assignees[0].Username)
	}
}

// TestWorkflowTemplate_Get_DEMO3ExternalStep verifies that the DEMO3 template
// (seeded in 0005) correctly returns a condition_type=3 step with NO assignees
// (external-signer steps have no workflow_step_assignees rows).
func TestWorkflowTemplate_Get_DEMO3ExternalStep(t *testing.T) {
	pool := validationPool(t)
	tmplID := resolveTemplateID(t, pool, "DEMO3", 1)

	r := newTmplRouter(pool, "system_admin")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET",
		fmt.Sprintf("/workflow-templates/%d", tmplID), nil))
	t.Logf("GET DEMO3 → %d %s", w.Code, w.Body.String())
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}

	var resp struct {
		Data struct {
			Steps []struct {
				SequenceNo    int    `json:"sequence_no"`
				PositionCode  string `json:"position_code"`
				ConditionType int    `json:"condition_type"`
				Assignees     []any  `json:"assignees"`
			} `json:"steps"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if len(resp.Data.Steps) != 3 {
		t.Fatalf("DEMO3: want 3 steps, got %d", len(resp.Data.Steps))
	}

	var externalStep *struct {
		SequenceNo    int
		PositionCode  string
		ConditionType int
		Assignees     []any
	}
	for _, s := range resp.Data.Steps {
		if s.ConditionType == 3 {
			cp := struct {
				SequenceNo    int
				PositionCode  string
				ConditionType int
				Assignees     []any
			}{s.SequenceNo, s.PositionCode, s.ConditionType, s.Assignees}
			externalStep = &cp
			break
		}
	}
	if externalStep == nil {
		t.Fatal("DEMO3: expected a condition_type=3 step")
	}
	if len(externalStep.Assignees) != 0 {
		t.Errorf("external step (condition_type=3): want 0 assignees, got %d", len(externalStep.Assignees))
	}
}

// TestWorkflowTemplate_Get_NotFound confirms 404 on a non-existent template id.
func TestWorkflowTemplate_Get_NotFound(t *testing.T) {
	pool := validationPool(t)
	r := newTmplRouter(pool, "workflow_admin")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/workflow-templates/999999999", nil))
	t.Logf("GET nonexistent → %d %s", w.Code, w.Body.String())
	if w.Code != http.StatusNotFound {
		t.Errorf("want 404, got %d", w.Code)
	}
	if got := errorCode(t, w.Body.String()); got != "not_found" {
		t.Errorf("code: want not_found, got %q", got)
	}
}

// TestWorkflowTemplate_Get_BadID confirms 400 on a non-integer id.
func TestWorkflowTemplate_Get_BadID(t *testing.T) {
	pool := validationPool(t)
	r := newTmplRouter(pool, "workflow_admin")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/workflow-templates/notanid", nil))
	t.Logf("GET bad id → %d %s", w.Code, w.Body.String())
	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400, got %d", w.Code)
	}
	if got := errorCode(t, w.Body.String()); got != "invalid_id" {
		t.Errorf("code: want invalid_id, got %q", got)
	}
}

// TestWorkflowTemplate_Get_BoundedAssignees verifies that a freshly-seeded
// template's detail returns the correct step/assignee structure — this proves the
// no-N+1 JOIN returns the right rows for a multi-step, multi-assignee template.
func TestWorkflowTemplate_Get_BoundedAssignees(t *testing.T) {
	pool := validationPool(t)
	tmplID := seedTemplateWithSteps(t, pool)

	r := newTmplRouter(pool, "workflow_admin")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET",
		fmt.Sprintf("/workflow-templates/%d", tmplID), nil))
	t.Logf("GET seeded tmpl → %d %s", w.Code, w.Body.String())
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Data struct {
			Steps []struct {
				PositionCode  string `json:"position_code"`
				ConditionType int    `json:"condition_type"`
				Assignees     []struct {
					Username     string `json:"username"`
					DisplayOrder *int   `json:"display_order"`
				} `json:"assignees"`
			} `json:"steps"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Data.Steps) != 2 {
		t.Fatalf("want 2 steps, got %d", len(resp.Data.Steps))
	}
	step1 := resp.Data.Steps[0]
	step2 := resp.Data.Steps[1]

	if step1.PositionCode != "MAKER" || step1.ConditionType != 1 {
		t.Errorf("step1: want MAKER/condition_type=1, got %q/%d", step1.PositionCode, step1.ConditionType)
	}
	if len(step1.Assignees) != 1 || step1.Assignees[0].Username != "maker" {
		t.Errorf("step1 assignees: want [maker], got %v", step1.Assignees)
	}

	if step2.PositionCode != "CHECKER" || step2.ConditionType != 2 {
		t.Errorf("step2: want CHECKER/condition_type=2, got %q/%d", step2.PositionCode, step2.ConditionType)
	}
	if len(step2.Assignees) != 2 {
		t.Fatalf("step2: want 2 assignees, got %d", len(step2.Assignees))
	}
	// Display order must be respected: checkerA (1) before checkerB (2).
	if step2.Assignees[0].Username != "checkerA" {
		t.Errorf("step2[0]: want checkerA, got %q", step2.Assignees[0].Username)
	}
	if step2.Assignees[1].Username != "checkerB" {
		t.Errorf("step2[1]: want checkerB, got %q", step2.Assignees[1].Username)
	}
	// display_order must be present on assignees.
	if step2.Assignees[0].DisplayOrder == nil || *step2.Assignees[0].DisplayOrder != 1 {
		t.Errorf("checkerA display_order: want 1, got %v", step2.Assignees[0].DisplayOrder)
	}
}

// TestWorkflowTemplate_List_Bounded confirms the list cap (200) is respected —
// seed 201 templates and assert the response is at most 200 rows.
func TestWorkflowTemplate_List_Bounded(t *testing.T) {
	pool := validationPool(t)
	ctx := context.Background()

	// Seed 205 draft templates under a unique format prefix.
	suffix := time.Now().UnixNano()
	prefix := fmt.Sprintf("BND%d", suffix)
	var ids []int64
	for i := 0; i < 205; i++ {
		var id int64
		fmtCode := fmt.Sprintf("%s%03d", prefix, i)
		if err := pool.QueryRow(ctx, `
			INSERT INTO workflow_templates (doc_format_code, name, version, status, created_by)
			SELECT $1, 'bound test', 1, 'draft', u.id FROM users u WHERE u.username='admin'
			RETURNING id
		`, fmtCode).Scan(&id); err != nil {
			t.Fatalf("seed bound tmpl %d: %v", i, err)
		}
		ids = append(ids, id)
	}
	t.Cleanup(func() {
		for _, id := range ids {
			_, _ = pool.Exec(ctx, `DELETE FROM workflow_templates WHERE id=$1`, id)
		}
	})

	r := newTmplRouter(pool, "workflow_admin")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET",
		fmt.Sprintf("/workflow-templates?doc_format_code=%s000", prefix), nil))
	// The filter narrows to 1 row — this is fine. We want to confirm the bound
	// applies globally, so filter to the prefix range by checking total rows.
	// Query without a filter to get all rows — cap must be 200.
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/workflow-templates", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	var resp struct {
		Data []any `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Data) > 200 {
		t.Errorf("list must cap at 200, got %d rows", len(resp.Data))
	}
}
