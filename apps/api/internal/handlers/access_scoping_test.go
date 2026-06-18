package handlers

// TestWorkflowStatus_AccessScoping and TestAttachmentList_AccessScoping prove
// that the horizontal-access leak is closed on the two previously-unguarded
// sub-endpoints.  They mirror TestDocumentGet_AccessScoping (document_detail_test.go)
// and are proven by running against a real DB (PAPERLESS_TEST_DB env var).
//
// Pattern: unassigned signer → 403 | assigned signer → 200 | admin → 200 | auditor → 200

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"paperless-api/internal/workflow"
)

// seedDocWithTask creates a doc + workflow step + signature_task assigned to
// assignedUserID. Returns docID. Distinct from seedDocWithTaskFor so there's no
// cross-file symbol collision (both are in the same test binary).
func seedDocWithTaskForScoping(t *testing.T, assignedUserID int64) int64 {
	t.Helper()
	pool := validationPool(t)
	ctx := context.Background()
	suffix := time.Now().UnixNano()
	fmtCode := fmt.Sprintf("SCOPE%d", suffix)

	var tmplID, stepID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO workflow_templates (doc_format_code, name, version, status, created_by)
		SELECT $1, 'scope test', 1, 'active', u.id FROM users u WHERE u.username='admin' RETURNING id
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
		VALUES ($1, 'SCOPE-001', 0, 9000000.00::numeric, $2, 1, 'pending', $3, 'scopehash') RETURNING id
	`, fmtCode, tmplID, fmt.Sprintf("SCOPE:%d:0", suffix)).Scan(&docID); err != nil {
		t.Fatalf("seed doc: %v", err)
	}
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

// ── GET /documents/:id/workflow-status ───────────────────────────────────────

func TestWorkflowStatus_AccessScoping(t *testing.T) {
	pool := validationPool(t)
	signerID := userIDByUsername(t, "maker")
	docID := seedDocWithTaskForScoping(t, signerID)

	eng := workflow.New(pool)
	gin.SetMode(gin.TestMode)
	ah := NewAuditHandler(pool, eng)

	run := func(userID int64, role string) *httptest.ResponseRecorder {
		r := gin.New()
		r.GET("/documents/:id/workflow-status", fakeAuth(userID, role), ah.WorkflowStatus)
		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", fmt.Sprintf("/documents/%d/workflow-status", docID), nil)
		r.ServeHTTP(w, req)
		return w
	}

	t.Run("assigned signer → 200", func(t *testing.T) {
		w := run(signerID, "signer")
		t.Logf("workflow-status assigned signer → %d %s", w.Code, w.Body.String())
		if w.Code != http.StatusOK {
			t.Errorf("want 200, got %d", w.Code)
		}
	})

	t.Run("unassigned signer → 403", func(t *testing.T) {
		otherID := userIDByUsername(t, "checkerA")
		if otherID == signerID {
			t.Skip("checkerA == maker, cannot test unassigned case")
		}
		w := run(otherID, "signer")
		t.Logf("workflow-status unassigned signer → %d %s", w.Code, w.Body.String())
		if w.Code != http.StatusForbidden {
			t.Errorf("want 403 (leak closed), got %d: %s", w.Code, w.Body.String())
		}
		if got := errorCode(t, w.Body.String()); got != "forbidden" {
			t.Errorf("code: want forbidden, got %q", got)
		}
	})

	t.Run("auditor (no task) → 200", func(t *testing.T) {
		w := run(9999, "auditor")
		t.Logf("workflow-status auditor → %d %s", w.Code, w.Body.String())
		if w.Code != http.StatusOK {
			t.Errorf("want 200, got %d", w.Code)
		}
	})

	t.Run("admin (no task) → 200", func(t *testing.T) {
		w := run(9999, "document_admin")
		t.Logf("workflow-status admin → %d %s", w.Code, w.Body.String())
		if w.Code != http.StatusOK {
			t.Errorf("want 200, got %d", w.Code)
		}
	})
}

// ── GET /documents/:id/attachments ───────────────────────────────────────────

func TestAttachmentList_AccessScoping(t *testing.T) {
	pool := validationPool(t)
	signerID := userIDByUsername(t, "maker")
	docID := seedDocWithTaskForScoping(t, signerID)

	gin.SetMode(gin.TestMode)
	ah := NewAttachmentHandler(pool, nil, zap.NewNop())

	run := func(userID int64, role string) *httptest.ResponseRecorder {
		r := gin.New()
		r.GET("/documents/:id/attachments", fakeAuth(userID, role), ah.List)
		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", fmt.Sprintf("/documents/%d/attachments", docID), nil)
		r.ServeHTTP(w, req)
		return w
	}

	t.Run("assigned signer → 200", func(t *testing.T) {
		w := run(signerID, "signer")
		t.Logf("attachments assigned signer → %d %s", w.Code, w.Body.String())
		if w.Code != http.StatusOK {
			t.Errorf("want 200, got %d", w.Code)
		}
	})

	t.Run("unassigned signer → 403", func(t *testing.T) {
		otherID := userIDByUsername(t, "checkerA")
		if otherID == signerID {
			t.Skip("checkerA == maker, cannot test unassigned case")
		}
		w := run(otherID, "signer")
		t.Logf("attachments unassigned signer → %d %s", w.Code, w.Body.String())
		if w.Code != http.StatusForbidden {
			t.Errorf("want 403 (leak closed), got %d: %s", w.Code, w.Body.String())
		}
		if got := errorCode(t, w.Body.String()); got != "forbidden" {
			t.Errorf("code: want forbidden, got %q", got)
		}
	})

	t.Run("auditor (no task) → 200", func(t *testing.T) {
		w := run(9999, "auditor")
		t.Logf("attachments auditor → %d %s", w.Code, w.Body.String())
		if w.Code != http.StatusOK {
			t.Errorf("want 200, got %d", w.Code)
		}
	})

	t.Run("admin (no task) → 200", func(t *testing.T) {
		w := run(9999, "document_admin")
		t.Logf("attachments admin → %d %s", w.Code, w.Body.String())
		if w.Code != http.StatusOK {
			t.Errorf("want 200, got %d", w.Code)
		}
	})
}

// ── GET /documents/:id/file/original ─────────────────────────────────────────
//
// Audit-found (Opus): DownloadOriginal/DownloadFinal leaked the actual PDF bytes
// to ANY authenticated user — higher severity than the metadata endpoints, and
// outside the original scope. These tests prove the boundary without MinIO:
//   - unassigned signer → 403 (access check fires before any storage/DB-file read)
//   - assigned signer / admin → NOT 403; they pass the access gate and reach the
//     file lookup, which returns 404 (no original_pdf row seeded). A non-403 code
//     here proves the access gate let them through. store is nil — safe because
//     the DB file-row lookup precedes any store call.

func TestDownloadOriginal_AccessScoping(t *testing.T) {
	pool := validationPool(t)
	signerID := userIDByUsername(t, "maker")
	docID := seedDocWithTaskForScoping(t, signerID)

	gin.SetMode(gin.TestMode)
	h := NewDocumentHandler(pool, nil, nil, zap.NewNop())

	run := func(userID int64, role string) *httptest.ResponseRecorder {
		r := gin.New()
		r.GET("/documents/:id/file/original", fakeAuth(userID, role), h.DownloadOriginal)
		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", fmt.Sprintf("/documents/%d/file/original", docID), nil)
		r.ServeHTTP(w, req)
		return w
	}

	t.Run("unassigned signer → 403 (PDF bytes leak closed)", func(t *testing.T) {
		otherID := userIDByUsername(t, "checkerA")
		if otherID == signerID {
			t.Skip("checkerA == maker")
		}
		w := run(otherID, "signer")
		t.Logf("download-original unassigned signer → %d %s", w.Code, w.Body.String())
		if w.Code != http.StatusForbidden {
			t.Errorf("want 403, got %d: %s", w.Code, w.Body.String())
		}
		if got := errorCode(t, w.Body.String()); got != "forbidden" {
			t.Errorf("code: want forbidden, got %q", got)
		}
	})

	t.Run("assigned signer passes access gate (not 403)", func(t *testing.T) {
		w := run(signerID, "signer")
		t.Logf("download-original assigned signer → %d %s", w.Code, w.Body.String())
		if w.Code == http.StatusForbidden {
			t.Errorf("assigned signer must NOT be blocked, got 403: %s", w.Body.String())
		}
		// No original_pdf row seeded → reaches 404 after passing the access gate.
		if w.Code != http.StatusNotFound {
			t.Errorf("expected 404 (no file row) after access gate, got %d", w.Code)
		}
	})

	t.Run("admin passes access gate (not 403)", func(t *testing.T) {
		w := run(9999, "document_admin")
		t.Logf("download-original admin → %d %s", w.Code, w.Body.String())
		if w.Code == http.StatusForbidden {
			t.Errorf("admin must NOT be blocked, got 403")
		}
	})
}

// ── GET /documents/:id/file/final ────────────────────────────────────────────

func TestDownloadFinal_AccessScoping(t *testing.T) {
	pool := validationPool(t)
	signerID := userIDByUsername(t, "maker")
	docID := seedDocWithTaskForScoping(t, signerID)

	gin.SetMode(gin.TestMode)
	h := NewDocumentHandler(pool, nil, nil, zap.NewNop())

	run := func(userID int64, role string) *httptest.ResponseRecorder {
		r := gin.New()
		r.GET("/documents/:id/file/final", fakeAuth(userID, role), h.DownloadFinal)
		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", fmt.Sprintf("/documents/%d/file/final", docID), nil)
		r.ServeHTTP(w, req)
		return w
	}

	t.Run("unassigned signer → 403 (signed-PDF leak closed)", func(t *testing.T) {
		otherID := userIDByUsername(t, "checkerA")
		if otherID == signerID {
			t.Skip("checkerA == maker")
		}
		w := run(otherID, "signer")
		t.Logf("download-final unassigned signer → %d %s", w.Code, w.Body.String())
		if w.Code != http.StatusForbidden {
			t.Errorf("want 403, got %d: %s", w.Code, w.Body.String())
		}
		if got := errorCode(t, w.Body.String()); got != "forbidden" {
			t.Errorf("code: want forbidden, got %q", got)
		}
	})

	t.Run("assigned signer passes access gate (not 403)", func(t *testing.T) {
		w := run(signerID, "signer")
		t.Logf("download-final assigned signer → %d %s", w.Code, w.Body.String())
		if w.Code == http.StatusForbidden {
			t.Errorf("assigned signer must NOT be blocked, got 403")
		}
		// doc is 'pending' (not completed) → 409 after passing access gate.
		if w.Code != http.StatusConflict {
			t.Errorf("expected 409 (not completed) after access gate, got %d", w.Code)
		}
	})
}
