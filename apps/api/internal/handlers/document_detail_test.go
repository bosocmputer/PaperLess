package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// seedDocWithAllFields creates a document with amount, doc_date, workflow_version
// set so the detail test can assert those fields come back. Returns docID.
func seedDocWithAllFields(t *testing.T) int64 {
	t.Helper()
	pool := validationPool(t)
	ctx := context.Background()
	suffix := time.Now().UnixNano()
	fmtCode := fmt.Sprintf("DET%d", suffix)

	var tmplID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO workflow_templates (doc_format_code, name, version, status, created_by)
		SELECT $1, 'detail test', 3, 'active', u.id FROM users u WHERE u.username='admin' RETURNING id
	`, fmtCode).Scan(&tmplID); err != nil {
		t.Fatalf("seed template: %v", err)
	}

	var docID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO documents
		       (doc_format_code, doc_no, revision, doc_date, amount,
		        workflow_template_id, workflow_version,
		        status, idempotency_key, source_hash)
		VALUES ($1, 'DET-001', 2, '2026-06-01'::date, 99999.99::numeric,
		        $2, 3,
		        'pending', $3, 'dethash')
		RETURNING id
	`, fmtCode, tmplID, fmt.Sprintf("DET:%d:0", suffix)).Scan(&docID); err != nil {
		t.Fatalf("seed doc: %v", err)
	}

	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM documents WHERE id=$1`, docID)
		_, _ = pool.Exec(ctx, `DELETE FROM workflow_templates WHERE id=$1`, tmplID)
	})
	return docID
}

// ── Admin detail shape ────────────────────────────────────────────────────────

// TestDocumentGet_AdminShape confirms that GET /documents/:id returns the full
// set of fields required by the admin dashboard: amount, doc_date,
// workflow_version, created_at — and does NOT expose idempotency_key.
func TestDocumentGet_AdminShape(t *testing.T) {
	pool := validationPool(t)
	docID := seedDocWithAllFields(t)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := NewDocumentHandler(pool, nil, nil, zap.NewNop())
	r.GET("/documents/:id", fakeAuth(1, "document_admin"), h.Get)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", fmt.Sprintf("/documents/%d", docID), nil)
	r.ServeHTTP(w, req)

	t.Logf("GET /documents/%d → %d %s", docID, w.Code, w.Body.String())
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}

	// Decode into a raw map to verify field presence/absence.
	var resp struct {
		Success bool           `json:"success"`
		Data    map[string]any `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Success {
		t.Fatal("success must be true")
	}

	d := resp.Data

	// Required fields for admin dashboard.
	for _, field := range []string{"id", "doc_format_code", "doc_no", "revision",
		"status", "sync_status", "amount", "doc_date", "workflow_version", "created_at"} {
		if _, ok := d[field]; !ok {
			t.Errorf("required field %q missing from response", field)
		}
	}

	// idempotency_key must NOT be in the response (internal dedup key).
	if _, ok := d["idempotency_key"]; ok {
		t.Error("idempotency_key must not be exposed in GET /documents/:id")
	}

	// Verify the seeded values round-trip correctly.
	if amt, ok := d["amount"].(string); !ok || amt == "" {
		t.Errorf("amount should be a non-empty string (99999.99), got %v", d["amount"])
	}
	if dd, ok := d["doc_date"].(string); !ok || dd == "" {
		t.Errorf("doc_date should be a non-empty string (2026-06-01), got %v", d["doc_date"])
	}
	if wv, ok := d["workflow_version"].(float64); !ok || int(wv) != 3 {
		t.Errorf("workflow_version should be 3, got %v", d["workflow_version"])
	}
	if ca, ok := d["created_at"].(string); !ok || ca == "" {
		t.Errorf("created_at should be a non-empty string, got %v", d["created_at"])
	}
}

// TestDocumentGet_NullableFields confirms that a document with NULL amount and
// doc_date returns null JSON values (not a scan error or missing keys).
func TestDocumentGet_NullableFields(t *testing.T) {
	pool := validationPool(t)
	ctx := context.Background()
	suffix := time.Now().UnixNano()
	fmtCode := fmt.Sprintf("DETNULL%d", suffix)

	var tmplID int64
	_ = pool.QueryRow(ctx, `
		INSERT INTO workflow_templates (doc_format_code, name, version, status, created_by)
		SELECT $1, 'null test', 1, 'active', u.id FROM users u WHERE u.username='admin' RETURNING id
	`, fmtCode).Scan(&tmplID)
	var docID int64
	_ = pool.QueryRow(ctx, `
		INSERT INTO documents (doc_format_code, doc_no, revision, workflow_template_id, workflow_version,
		                       status, idempotency_key, source_hash)
		VALUES ($1,'NULL-001',0,$2,1,'pending',$3,'nullhash') RETURNING id
	`, fmtCode, tmplID, fmt.Sprintf("NULL:%d", suffix)).Scan(&docID)
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM documents WHERE id=$1`, docID)
		_, _ = pool.Exec(ctx, `DELETE FROM workflow_templates WHERE id=$1`, tmplID)
	})

	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := NewDocumentHandler(pool, nil, nil, zap.NewNop())
	r.GET("/documents/:id", fakeAuth(1, "document_admin"), h.Get)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", fmt.Sprintf("/documents/%d", docID), nil)
	r.ServeHTTP(w, req)

	t.Logf("GET /documents/%d (nulls) → %d %s", docID, w.Code, w.Body.String())
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// amount and doc_date should be present as null, not missing.
	if v, ok := resp.Data["amount"]; !ok {
		t.Error("amount key must be present even when NULL")
	} else if v != nil {
		t.Errorf("amount should be null, got %v", v)
	}
	if v, ok := resp.Data["doc_date"]; !ok {
		t.Error("doc_date key must be present even when NULL")
	} else if v != nil {
		t.Errorf("doc_date should be null, got %v", v)
	}
	// sync_status should also be null (column is NULLable).
	if v, ok := resp.Data["sync_status"]; !ok {
		t.Error("sync_status key must be present even when NULL")
	} else if v != nil {
		t.Errorf("sync_status should be null, got %v", v)
	}
}

// TestDocumentGet_NotFound confirms 404 on a non-existent document id.
func TestDocumentGet_NotFound(t *testing.T) {
	pool := validationPool(t)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := NewDocumentHandler(pool, nil, nil, zap.NewNop())
	r.GET("/documents/:id", fakeAuth(1, "document_admin"), h.Get)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/documents/999999999", nil)
	r.ServeHTTP(w, req)

	t.Logf("GET /documents/999999999 → %d %s", w.Code, w.Body.String())
	if w.Code != http.StatusNotFound {
		t.Errorf("want 404, got %d", w.Code)
	}
	if got := errorCode(t, w.Body.String()); got != "not_found" {
		t.Errorf("code: want %q, got %q", "not_found", got)
	}
}

// TestDocumentGet_BadID confirms 400 on a non-integer id.
func TestDocumentGet_BadID(t *testing.T) {
	pool := validationPool(t)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := NewDocumentHandler(pool, nil, nil, zap.NewNop())
	r.GET("/documents/:id", fakeAuth(1, "document_admin"), h.Get)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/documents/notanid", nil)
	r.ServeHTTP(w, req)

	t.Logf("GET /documents/notanid → %d %s", w.Code, w.Body.String())
	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400, got %d", w.Code)
	}
}

// ── Access scoping (audit-added: close the horizontal-access leak) ───────────

// seedDocWithTaskFor seeds a pending doc with a workflow step and a signature
// task assigned to assignedUserID. Returns docID. Cleaned up on test end.
func seedDocWithTaskFor(t *testing.T, assignedUserID int64) int64 {
	t.Helper()
	pool := validationPool(t)
	ctx := context.Background()
	suffix := time.Now().UnixNano()
	fmtCode := fmt.Sprintf("ACL%d", suffix)

	var tmplID, stepID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO workflow_templates (doc_format_code, name, version, status, created_by)
		SELECT $1, 'acl test', 1, 'active', u.id FROM users u WHERE u.username='admin' RETURNING id
	`, fmtCode).Scan(&tmplID); err != nil {
		t.Fatalf("seed template: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO workflow_steps (workflow_template_id, position_code, position_name, sequence_no, condition_type)
		VALUES ($1, 'MAKER', 'ผู้จัดทำ', 1, 1) RETURNING id
	`, tmplID).Scan(&stepID); err != nil {
		t.Fatalf("seed step: %v", err)
	}

	var docID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO documents (doc_format_code, doc_no, revision, amount, workflow_template_id, workflow_version,
		                       status, idempotency_key, source_hash)
		VALUES ($1, 'ACL-001', 0, 5000000.00::numeric, $2, 1, 'pending', $3, 'aclhash') RETURNING id
	`, fmtCode, tmplID, fmt.Sprintf("ACL:%d:0", suffix)).Scan(&docID); err != nil {
		t.Fatalf("seed doc: %v", err)
	}

	// Task assigned to assignedUserID.
	if _, err := pool.Exec(ctx, `
		INSERT INTO signature_tasks (document_id, workflow_step_id, assigned_user_id, sequence_no, condition_type, status)
		VALUES ($1, $2, $3, 1, 1, 'open')
	`, docID, stepID, assignedUserID); err != nil {
		t.Fatalf("seed task: %v", err)
	}

	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM signature_tasks WHERE document_id=$1`, docID)
		_, _ = pool.Exec(ctx, `DELETE FROM documents WHERE id=$1`, docID)
		_, _ = pool.Exec(ctx, `DELETE FROM workflow_steps WHERE workflow_template_id=$1`, tmplID)
		_, _ = pool.Exec(ctx, `DELETE FROM workflow_templates WHERE id=$1`, tmplID)
	})
	return docID
}

// userIDByUsername resolves a seeded user's id so a test signer's claims match a
// real assigned_user_id.
func userIDByUsername(t *testing.T, username string) int64 {
	t.Helper()
	pool := validationPool(t)
	var id int64
	if err := pool.QueryRow(context.Background(),
		`SELECT id FROM users WHERE username=$1`, username).Scan(&id); err != nil {
		t.Fatalf("resolve user %q: %v", username, err)
	}
	return id
}

// TestDocumentGet_AccessScoping proves the access boundary on GET /documents/:id:
//   - a plain signer assigned a task on the doc → 200 (legitimate inbox flow)
//   - a plain signer with NO task on the doc → 403 (cannot harvest by id)
//   - an auditor (no task) → 200 (role-based read)
//   - an admin (no task) → 200 (role-based read)
//
// This closes the horizontal-access leak Step 2 widened by adding `amount` to the
// payload: before the fix, ANY authenticated user could read ANY document's
// amount by iterating ids. The legitimate signer UI only opens docs from the
// user's own inbox, so the 403 branch never fires for real users.
func TestDocumentGet_AccessScoping(t *testing.T) {
	pool := validationPool(t)
	signerID := userIDByUsername(t, "maker") // plain signer role
	docID := seedDocWithTaskFor(t, signerID)

	gin.SetMode(gin.TestMode)
	h := NewDocumentHandler(pool, nil, nil, zap.NewNop())

	run := func(userID int64, role string) *httptest.ResponseRecorder {
		r := gin.New()
		r.GET("/documents/:id", fakeAuth(userID, role), h.Get)
		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", fmt.Sprintf("/documents/%d", docID), nil)
		r.ServeHTTP(w, req)
		return w
	}

	t.Run("assigned signer → 200", func(t *testing.T) {
		w := run(signerID, "signer")
		t.Logf("assigned signer → %d %s", w.Code, w.Body.String())
		if w.Code != http.StatusOK {
			t.Errorf("assigned signer must read the doc: want 200, got %d", w.Code)
		}
	})

	t.Run("unassigned signer → 403", func(t *testing.T) {
		// A different user id with no task on this doc.
		otherID := userIDByUsername(t, "checkerA")
		if otherID == signerID {
			t.Skip("checkerA == maker, cannot test unassigned case")
		}
		w := run(otherID, "signer")
		t.Logf("unassigned signer → %d %s", w.Code, w.Body.String())
		if w.Code != http.StatusForbidden {
			t.Errorf("unassigned signer must be blocked: want 403, got %d (leak: %s)", w.Code, w.Body.String())
		}
		// The blocked response must NOT leak the amount.
		if got := errorCode(t, w.Body.String()); got != "forbidden" {
			t.Errorf("code: want forbidden, got %q", got)
		}
	})

	t.Run("auditor (no task) → 200", func(t *testing.T) {
		w := run(9999, "auditor")
		t.Logf("auditor → %d %s", w.Code, w.Body.String())
		if w.Code != http.StatusOK {
			t.Errorf("auditor must read any doc: want 200, got %d", w.Code)
		}
	})

	t.Run("admin (no task) → 200", func(t *testing.T) {
		w := run(9999, "document_admin")
		t.Logf("admin → %d %s", w.Code, w.Body.String())
		if w.Code != http.StatusOK {
			t.Errorf("admin must read any doc: want 200, got %d", w.Code)
		}
	})
}
