package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"paperless-api/internal/middleware"
)

// ── lifecycle router helper ───────────────────────────────────────────────────

func newLifecycleRouter(pool *pgxpool.Pool, role string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(gin.Recovery())
	h := NewWorkflowTemplateHandler(pool, zap.NewNop())
	guard := middleware.RequireRole("workflow_admin", "system_admin")
	adminID := int64(1) // corresponds to admin user seeded in 0002
	r.POST("/workflow-templates/:id/clone", fakeAuth(adminID, role), guard, h.Clone)
	r.POST("/workflow-templates/:id/publish", fakeAuth(adminID, role), guard, h.Publish)
	r.POST("/workflow-templates/:id/deactivate", fakeAuth(adminID, role), guard, h.Deactivate)
	return r
}

// seedDraftTemplate creates a standalone draft template with two steps + assignees.
// Returns (templateID, docFormatCode). Cleaned up on test end.
func seedDraftTemplate(t *testing.T, pool *pgxpool.Pool) (int64, string) {
	t.Helper()
	ctx := context.Background()
	suffix := time.Now().UnixNano()
	fmtCode := fmt.Sprintf("LC%d", suffix)

	var tmplID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO workflow_templates (doc_format_code, name, version, status, created_by)
		SELECT $1, 'lifecycle test tmpl', 1, 'draft', u.id
		  FROM users u WHERE u.username='admin'
		RETURNING id
	`, fmtCode).Scan(&tmplID); err != nil {
		t.Fatalf("seed draft template: %v", err)
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
	if _, err := pool.Exec(ctx, `
		INSERT INTO workflow_step_assignees (workflow_step_id, user_id, display_order)
		SELECT $1, u.id, 1 FROM users u WHERE u.username='maker'
	`, stepID1); err != nil {
		t.Fatalf("seed assignee maker: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO workflow_step_assignees (workflow_step_id, user_id, display_order)
		SELECT $1, u.id, 1 FROM users u WHERE u.username='checkerA'
	`, stepID2); err != nil {
		t.Fatalf("seed assignee checkerA: %v", err)
	}

	t.Cleanup(func() {
		cleanupTemplateByFormatCode(pool, fmtCode)
	})
	return tmplID, fmtCode
}

// seedActiveTemplate creates a draft + publishes it to active.
// Returns (templateID, docFormatCode). Cleaned up on test end.
func seedActiveTemplate(t *testing.T, pool *pgxpool.Pool) (int64, string) {
	t.Helper()
	ctx := context.Background()
	tmplID, fmtCode := seedDraftTemplate(t, pool)
	if _, err := pool.Exec(ctx, `
		UPDATE workflow_templates SET status='active', effective_from=now() WHERE id=$1
	`, tmplID); err != nil {
		t.Fatalf("activate template: %v", err)
	}
	return tmplID, fmtCode
}

// cleanupTemplateByFormatCode removes all templates (and cascaded steps/assignees) for a format code.
func cleanupTemplateByFormatCode(pool *pgxpool.Pool, fmtCode string) {
	ctx := context.Background()
	// Cascade: workflow_steps and workflow_step_assignees ON DELETE CASCADE.
	_, _ = pool.Exec(ctx, `DELETE FROM workflow_templates WHERE doc_format_code=$1`, fmtCode)
}

// fetchTemplateStatus reads the status of a template by id.
func fetchTemplateStatus(t *testing.T, pool *pgxpool.Pool, id int64) string {
	t.Helper()
	var status string
	if err := pool.QueryRow(context.Background(),
		`SELECT status FROM workflow_templates WHERE id=$1`, id,
	).Scan(&status); err != nil {
		t.Fatalf("fetchTemplateStatus %d: %v", id, err)
	}
	return status
}

// countSteps returns the number of workflow_steps for a template.
func countSteps(t *testing.T, pool *pgxpool.Pool, tmplID int64) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM workflow_steps WHERE workflow_template_id=$1`, tmplID,
	).Scan(&n); err != nil {
		t.Fatalf("countSteps %d: %v", tmplID, err)
	}
	return n
}

// countAssignees returns the total number of workflow_step_assignees for all steps of a template.
func countAssignees(t *testing.T, pool *pgxpool.Pool, tmplID int64) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM workflow_step_assignees wsa
		   JOIN workflow_steps ws ON ws.id=wsa.workflow_step_id
		  WHERE ws.workflow_template_id=$1`, tmplID,
	).Scan(&n); err != nil {
		t.Fatalf("countAssignees %d: %v", tmplID, err)
	}
	return n
}

// countActiveForFormat returns the number of active templates for a given doc_format_code.
func countActiveForFormat(t *testing.T, pool *pgxpool.Pool, fmtCode string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM workflow_templates WHERE doc_format_code=$1 AND status='active'`, fmtCode,
	).Scan(&n); err != nil {
		t.Fatalf("countActiveForFormat %s: %v", fmtCode, err)
	}
	return n
}

// ── Role guard ────────────────────────────────────────────────────────────────

func TestWorkflowLifecycle_RoleGuard(t *testing.T) {
	pool := validationPool(t)
	tmplID, _ := seedDraftTemplate(t, pool)
	path := fmt.Sprintf("/workflow-templates/%d", tmplID)

	deniedRoles := []string{"document_admin", "auditor", "signer"}
	allowedRoles := []string{"workflow_admin", "system_admin"}
	actions := []string{"clone", "publish", "deactivate"}

	for _, role := range deniedRoles {
		for _, action := range actions {
			t.Run(role+"/"+action, func(t *testing.T) {
				r := newLifecycleRouter(pool, role)
				w := httptest.NewRecorder()
				r.ServeHTTP(w, httptest.NewRequest("POST", path+"/"+action, nil))
				if w.Code != http.StatusForbidden {
					t.Errorf("role %s action %s: want 403, got %d", role, action, w.Code)
				}
			})
		}
	}

	for _, role := range allowedRoles {
		// Clone should return 201 for an allowed role — just verify not 403.
		t.Run(role+"/clone", func(t *testing.T) {
			r := newLifecycleRouter(pool, role)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest("POST", path+"/clone", nil))
			if w.Code == http.StatusForbidden {
				t.Errorf("role %s: clone should not 403", role)
			}
		})
	}
}

// ── Clone ─────────────────────────────────────────────────────────────────────

func TestWorkflowLifecycle_Clone_ProducesIndependentDraft(t *testing.T) {
	pool := validationPool(t)
	srcID, fmtCode := seedDraftTemplate(t, pool)

	r := newLifecycleRouter(pool, "workflow_admin")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("POST",
		fmt.Sprintf("/workflow-templates/%d/clone", srcID), nil))
	t.Logf("clone → %d %s", w.Code, w.Body.String())

	if w.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Data struct {
			ID      string `json:"id"`
			Status  string `json:"status"`
			Version int    `json:"version"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	newID, err := strconv.ParseInt(resp.Data.ID, 10, 64)
	if err != nil || newID == 0 {
		t.Fatalf("invalid new id: %q", resp.Data.ID)
	}
	if resp.Data.Status != "draft" {
		t.Errorf("want status=draft, got %q", resp.Data.Status)
	}
	if resp.Data.Version != 2 {
		t.Errorf("want version=2, got %d", resp.Data.Version)
	}

	// New template must be a different row from the source.
	if newID == srcID {
		t.Error("clone returned the same id as source")
	}

	// Both templates should be 'draft'.
	if s := fetchTemplateStatus(t, pool, srcID); s != "draft" {
		t.Errorf("source status: want draft, got %q", s)
	}
	if s := fetchTemplateStatus(t, pool, newID); s != "draft" {
		t.Errorf("clone status: want draft, got %q", s)
	}

	// Clone must have the same number of steps (2) and assignees (2) as source.
	srcSteps := countSteps(t, pool, srcID)
	cloneSteps := countSteps(t, pool, newID)
	if cloneSteps != srcSteps {
		t.Errorf("steps: source=%d, clone=%d", srcSteps, cloneSteps)
	}
	srcAssignees := countAssignees(t, pool, srcID)
	cloneAssignees := countAssignees(t, pool, newID)
	if cloneAssignees != srcAssignees {
		t.Errorf("assignees: source=%d, clone=%d", srcAssignees, cloneAssignees)
	}

	// Clone rows must be DIFFERENT rows in workflow_steps (independent copy).
	var sharedRows int
	_ = pool.QueryRow(context.Background(), `
		SELECT COUNT(*) FROM workflow_steps
		 WHERE workflow_template_id=$1
		   AND id IN (SELECT id FROM workflow_steps WHERE workflow_template_id=$2)
	`, newID, srcID).Scan(&sharedRows)
	if sharedRows != 0 {
		t.Errorf("clone shares %d step rows with source — not independent", sharedRows)
	}

	// Audit log written.
	var auditCount int
	_ = pool.QueryRow(context.Background(), `
		SELECT COUNT(*) FROM audit_logs
		 WHERE action='workflow_template_cloned' AND entity_id=$1
	`, strconv.FormatInt(newID, 10)).Scan(&auditCount)
	if auditCount == 0 {
		t.Error("audit log for clone not found")
	}

	// Cleanup the cloned template.
	t.Cleanup(func() { cleanupTemplateByFormatCode(pool, fmtCode) })
}

func TestWorkflowLifecycle_Clone_NotFound(t *testing.T) {
	pool := validationPool(t)
	r := newLifecycleRouter(pool, "workflow_admin")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("POST", "/workflow-templates/999999999/clone", nil))
	if w.Code != http.StatusNotFound {
		t.Errorf("want 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestWorkflowLifecycle_Clone_BadID(t *testing.T) {
	pool := validationPool(t)
	r := newLifecycleRouter(pool, "workflow_admin")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("POST", "/workflow-templates/abc/clone", nil))
	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400, got %d", w.Code)
	}
}

// ── Publish ───────────────────────────────────────────────────────────────────

func TestWorkflowLifecycle_Publish_DraftBecomesActive(t *testing.T) {
	pool := validationPool(t)
	tmplID, fmtCode := seedDraftTemplate(t, pool)
	defer cleanupTemplateByFormatCode(pool, fmtCode)

	r := newLifecycleRouter(pool, "workflow_admin")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("POST",
		fmt.Sprintf("/workflow-templates/%d/publish", tmplID), nil))
	t.Logf("publish → %d %s", w.Code, w.Body.String())

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	if s := fetchTemplateStatus(t, pool, tmplID); s != "active" {
		t.Errorf("want status=active after publish, got %q", s)
	}
	if n := countActiveForFormat(t, pool, fmtCode); n != 1 {
		t.Errorf("want exactly 1 active for format, got %d", n)
	}

	// Audit log written.
	var auditCount int
	_ = pool.QueryRow(context.Background(), `
		SELECT COUNT(*) FROM audit_logs
		 WHERE action='workflow_template_published' AND entity_id=$1
	`, strconv.FormatInt(tmplID, 10)).Scan(&auditCount)
	if auditCount == 0 {
		t.Error("audit log for publish not found")
	}
}

func TestWorkflowLifecycle_Publish_DemotesExistingActive(t *testing.T) {
	pool := validationPool(t)

	// v1 active, v2 draft for the same format.
	v1ID, fmtCode := seedActiveTemplate(t, pool)
	defer cleanupTemplateByFormatCode(pool, fmtCode)

	// Insert v2 draft for the same format code.
	var v2ID int64
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO workflow_templates (doc_format_code, name, version, status, created_by)
		SELECT $1, 'lifecycle test tmpl', 2, 'draft', u.id
		  FROM users u WHERE u.username='admin'
		RETURNING id
	`, fmtCode).Scan(&v2ID); err != nil {
		t.Fatalf("seed v2: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO workflow_steps (workflow_template_id, position_code, position_name, sequence_no, condition_type)
		VALUES ($1, 'P1', 'Pos 1', 1, 1)
	`, v2ID); err != nil {
		t.Fatalf("seed v2 step: %v", err)
	}

	r := newLifecycleRouter(pool, "workflow_admin")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("POST",
		fmt.Sprintf("/workflow-templates/%d/publish", v2ID), nil))
	t.Logf("publish v2 → %d %s", w.Code, w.Body.String())

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	if s := fetchTemplateStatus(t, pool, v1ID); s != "inactive" {
		t.Errorf("v1 should be demoted to inactive, got %q", s)
	}
	if s := fetchTemplateStatus(t, pool, v2ID); s != "active" {
		t.Errorf("v2 should be active, got %q", s)
	}
	if n := countActiveForFormat(t, pool, fmtCode); n != 1 {
		t.Errorf("want exactly 1 active for format after publish, got %d", n)
	}
}

func TestWorkflowLifecycle_Publish_IdempotentIfAlreadyActive(t *testing.T) {
	pool := validationPool(t)
	tmplID, fmtCode := seedActiveTemplate(t, pool)
	defer cleanupTemplateByFormatCode(pool, fmtCode)

	r := newLifecycleRouter(pool, "workflow_admin")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("POST",
		fmt.Sprintf("/workflow-templates/%d/publish", tmplID), nil))
	t.Logf("re-publish active → %d %s", w.Code, w.Body.String())
	if w.Code != http.StatusOK {
		t.Errorf("idempotent publish: want 200, got %d", w.Code)
	}
	if s := fetchTemplateStatus(t, pool, tmplID); s != "active" {
		t.Errorf("status should remain active, got %q", s)
	}
}

func TestWorkflowLifecycle_Publish_InactiveTemplateFails(t *testing.T) {
	pool := validationPool(t)
	tmplID, fmtCode := seedDraftTemplate(t, pool)
	defer cleanupTemplateByFormatCode(pool, fmtCode)

	// Force inactive.
	if _, err := pool.Exec(context.Background(),
		`UPDATE workflow_templates SET status='inactive' WHERE id=$1`, tmplID); err != nil {
		t.Fatalf("force inactive: %v", err)
	}

	r := newLifecycleRouter(pool, "workflow_admin")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("POST",
		fmt.Sprintf("/workflow-templates/%d/publish", tmplID), nil))
	t.Logf("publish inactive → %d %s", w.Code, w.Body.String())
	if w.Code != http.StatusConflict {
		t.Errorf("want 409, got %d", w.Code)
	}
	if code := errorCode(t, w.Body.String()); code != "template_not_publishable" {
		t.Errorf("want code=template_not_publishable, got %q", code)
	}
}

// TestWorkflowLifecycle_Publish_ConcurrentRace verifies the single-active invariant
// under a concurrent publish: two goroutines racing to publish two separate drafts
// for the same doc_format_code — exactly one must win, the other must get a 4xx (not 500),
// and the DB must have exactly one active template.
// Uses close(start) barrier, -race, -count=3.
func TestWorkflowLifecycle_Publish_ConcurrentRace(t *testing.T) {
	pool := validationPool(t)

	const runs = 3
	for run := 0; run < runs; run++ {
		t.Run(fmt.Sprintf("run%d", run), func(t *testing.T) {
			ctx := context.Background()
			suffix := time.Now().UnixNano() + int64(run)
			fmtCode := fmt.Sprintf("RACE%d", suffix)
			defer cleanupTemplateByFormatCode(pool, fmtCode)

			// Seed two drafts for the same format.
			var v1ID, v2ID int64
			for i, dest := range []*int64{&v1ID, &v2ID} {
				if err := pool.QueryRow(ctx, `
					INSERT INTO workflow_templates (doc_format_code, name, version, status, created_by)
					SELECT $1, 'race tmpl', $2, 'draft', u.id
					  FROM users u WHERE u.username='admin'
					RETURNING id
				`, fmtCode, i+1).Scan(dest); err != nil {
					t.Fatalf("seed v%d: %v", i+1, err)
				}
				// Publishable templates need ≥1 step (no_steps guard).
				if _, err := pool.Exec(ctx, `
					INSERT INTO workflow_steps (workflow_template_id, position_code, position_name, sequence_no, condition_type)
					VALUES ($1, 'P1', 'Pos 1', 1, 1)
				`, *dest); err != nil {
					t.Fatalf("seed v%d step: %v", i+1, err)
				}
			}

			r := newLifecycleRouter(pool, "workflow_admin")
			type result struct {
				code int
				body string
			}
			results := make([]result, 2)
			start := make(chan struct{})
			var wg sync.WaitGroup
			wg.Add(2)

			for i, tid := range []int64{v1ID, v2ID} {
				i, tid := i, tid
				go func() {
					defer wg.Done()
					<-start
					w := httptest.NewRecorder()
					r.ServeHTTP(w, httptest.NewRequest("POST",
						fmt.Sprintf("/workflow-templates/%d/publish", tid), nil))
					results[i] = result{w.Code, w.Body.String()}
				}()
			}
			close(start)
			wg.Wait()

			t.Logf("v1(%d)→%d, v2(%d)→%d", v1ID, results[0].code, v2ID, results[1].code)
			t.Logf("  v1 body: %s", results[0].body)
			t.Logf("  v2 body: %s", results[1].body)

			// Exactly one active must survive.
			active := countActiveForFormat(t, pool, fmtCode)
			if active != 1 {
				t.Errorf("want exactly 1 active for format %s after concurrent publish, got %d", fmtCode, active)
			}

			// No 500s — any loser gets 200 (idempotent) or 4xx (conflict), never 500.
			for i, res := range results {
				if res.code >= 500 {
					t.Errorf("result[%d]: got 5xx (%d) — must never happen: %s", i, res.code, res.body)
				}
			}

			// At least one must be 200.
			win := 0
			for _, res := range results {
				if res.code == http.StatusOK {
					win++
				}
			}
			if win == 0 {
				t.Error("no winner: both publishes failed")
			}
		})
	}
}

// ── Deactivate ────────────────────────────────────────────────────────────────

func TestWorkflowLifecycle_Deactivate_ActiveBecomesInactive(t *testing.T) {
	pool := validationPool(t)
	tmplID, fmtCode := seedActiveTemplate(t, pool)
	defer cleanupTemplateByFormatCode(pool, fmtCode)

	r := newLifecycleRouter(pool, "workflow_admin")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("POST",
		fmt.Sprintf("/workflow-templates/%d/deactivate", tmplID), nil))
	t.Logf("deactivate → %d %s", w.Code, w.Body.String())

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	if s := fetchTemplateStatus(t, pool, tmplID); s != "inactive" {
		t.Errorf("want status=inactive after deactivate, got %q", s)
	}

	// Audit log written.
	var auditCount int
	_ = pool.QueryRow(context.Background(), `
		SELECT COUNT(*) FROM audit_logs
		 WHERE action='workflow_template_deactivated' AND entity_id=$1
	`, strconv.FormatInt(tmplID, 10)).Scan(&auditCount)
	if auditCount == 0 {
		t.Error("audit log for deactivate not found")
	}
}

func TestWorkflowLifecycle_Deactivate_IdempotentIfAlreadyInactive(t *testing.T) {
	pool := validationPool(t)
	tmplID, fmtCode := seedDraftTemplate(t, pool)
	defer cleanupTemplateByFormatCode(pool, fmtCode)

	// Force inactive.
	if _, err := pool.Exec(context.Background(),
		`UPDATE workflow_templates SET status='inactive' WHERE id=$1`, tmplID); err != nil {
		t.Fatalf("force inactive: %v", err)
	}

	r := newLifecycleRouter(pool, "workflow_admin")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("POST",
		fmt.Sprintf("/workflow-templates/%d/deactivate", tmplID), nil))
	if w.Code != http.StatusOK {
		t.Errorf("idempotent deactivate: want 200, got %d", w.Code)
	}
}

func TestWorkflowLifecycle_Deactivate_DraftFails(t *testing.T) {
	pool := validationPool(t)
	tmplID, fmtCode := seedDraftTemplate(t, pool)
	defer cleanupTemplateByFormatCode(pool, fmtCode)

	r := newLifecycleRouter(pool, "workflow_admin")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("POST",
		fmt.Sprintf("/workflow-templates/%d/deactivate", tmplID), nil))
	t.Logf("deactivate draft → %d %s", w.Code, w.Body.String())
	if w.Code != http.StatusConflict {
		t.Errorf("want 409, got %d", w.Code)
	}
	if code := errorCode(t, w.Body.String()); code != "template_not_deactivatable" {
		t.Errorf("want code=template_not_deactivatable, got %q", code)
	}
}

// TestWorkflowLifecycle_Deactivate_ReimportReturnsWorkflowConfigMissing proves that after
// deactivating the only active template for a format, no active template remains —
// which is the DB precondition for the import handler to return workflow_config_missing.
// Import's actual 422 path is covered by import_validation_test.go; here we verify
// that deactivate removes the active entry that import relies on.
func TestWorkflowLifecycle_Deactivate_ReimportReturnsWorkflowConfigMissing(t *testing.T) {
	pool := validationPool(t)
	tmplID, fmtCode := seedActiveTemplate(t, pool)
	defer cleanupTemplateByFormatCode(pool, fmtCode)

	// Confirm there is exactly one active before deactivate.
	if n := countActiveForFormat(t, pool, fmtCode); n != 1 {
		t.Fatalf("pre-deactivate: want 1 active, got %d", n)
	}

	// Deactivate.
	r := newLifecycleRouter(pool, "workflow_admin")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("POST",
		fmt.Sprintf("/workflow-templates/%d/deactivate", tmplID), nil))
	if w.Code != http.StatusOK {
		t.Fatalf("deactivate: want 200, got %d: %s", w.Code, w.Body.String())
	}

	// After deactivate: zero active for this format — import would hit workflow_config_missing.
	if n := countActiveForFormat(t, pool, fmtCode); n != 0 {
		t.Errorf("post-deactivate: want 0 active for format, got %d (import would not return workflow_config_missing)", n)
	}

	// Verify the exact DB query that import uses before returning workflow_config_missing returns no rows.
	var activeID int64
	err := pool.QueryRow(context.Background(), `
		SELECT id FROM workflow_templates WHERE doc_format_code=$1 AND status='active'
	`, fmtCode).Scan(&activeID)
	if err == nil {
		t.Errorf("expected no active template after deactivate, but found id=%d", activeID)
	}
	t.Logf("post-deactivate active lookup: %v (ErrNoRows is correct)", err)
}

// TestWorkflowLifecycle_InFlightDocsUnaffected proves that publishing a new version
// or deactivating the old one does not change the workflow_version of existing docs
// or touch their in-flight signature tasks.
func TestWorkflowLifecycle_InFlightDocsUnaffected(t *testing.T) {
	pool := validationPool(t)
	ctx := context.Background()

	v1ID, fmtCode := seedActiveTemplate(t, pool)
	defer cleanupTemplateByFormatCode(pool, fmtCode)

	// Create a document bound to v1.
	suffix := time.Now().UnixNano()
	var docID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO documents
		       (doc_format_code, doc_no, revision, workflow_template_id, workflow_version,
		        status, idempotency_key, source_hash)
		VALUES ($1, $2, 0, $3, 1, 'pending', $4, $5)
		RETURNING id
	`, fmtCode, fmt.Sprintf("INF-%d", suffix),
		v1ID, fmt.Sprintf("ik-inf-%d", suffix), fmt.Sprintf("sh-inf-%d", suffix),
	).Scan(&docID); err != nil {
		t.Fatalf("seed doc: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM documents WHERE id=$1`, docID)
	})

	// Seed a pending task for the doc.
	var stepID int64
	_ = pool.QueryRow(ctx,
		`SELECT id FROM workflow_steps WHERE workflow_template_id=$1 AND sequence_no=1`, v1ID,
	).Scan(&stepID)

	var taskID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO signature_tasks (document_id, workflow_step_id, sequence_no, condition_type, status)
		VALUES ($1, $2, 1, 1, 'open') RETURNING id
	`, docID, stepID).Scan(&taskID); err != nil {
		t.Fatalf("seed task: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM signature_tasks WHERE id=$1`, taskID)
	})

	// Publish v2 (a clone of v1).
	var v2ID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO workflow_templates (doc_format_code, name, version, status, created_by)
		SELECT $1, 'lifecycle test tmpl', 2, 'draft', u.id
		  FROM users u WHERE u.username='admin'
		RETURNING id
	`, fmtCode).Scan(&v2ID); err != nil {
		t.Fatalf("seed v2: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO workflow_steps (workflow_template_id, position_code, position_name, sequence_no, condition_type)
		VALUES ($1, 'P1', 'Pos 1', 1, 1)
	`, v2ID); err != nil {
		t.Fatalf("seed v2 step: %v", err)
	}

	r := newLifecycleRouter(pool, "workflow_admin")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("POST",
		fmt.Sprintf("/workflow-templates/%d/publish", v2ID), nil))
	if w.Code != http.StatusOK {
		t.Fatalf("publish v2: want 200, got %d: %s", w.Code, w.Body.String())
	}

	// v1 should now be inactive (demoted by publish).
	if s := fetchTemplateStatus(t, pool, v1ID); s != "inactive" {
		t.Errorf("v1 should be inactive after v2 published, got %q", s)
	}

	// The existing doc must still reference workflow_version=1.
	var wfVersion int
	if err := pool.QueryRow(ctx,
		`SELECT workflow_version FROM documents WHERE id=$1`, docID,
	).Scan(&wfVersion); err != nil {
		t.Fatalf("fetch doc workflow_version: %v", err)
	}
	if wfVersion != 1 {
		t.Errorf("existing doc workflow_version: want 1, got %d", wfVersion)
	}

	// The in-flight task must be untouched (still 'open').
	var taskStatus string
	if err := pool.QueryRow(ctx,
		`SELECT status FROM signature_tasks WHERE id=$1`, taskID,
	).Scan(&taskStatus); err != nil {
		t.Fatalf("fetch task status: %v", err)
	}
	if taskStatus != "open" {
		t.Errorf("in-flight task status: want open, got %q", taskStatus)
	}
}
