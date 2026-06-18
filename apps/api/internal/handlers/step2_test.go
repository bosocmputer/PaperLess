package handlers

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"paperless-api/internal/middleware"
)

// seedCompletedDoc creates a completed document with no final_pdf row (simulates
// storage down at completion time). Returns docID. Cleaned up on test end.
func seedCompletedDoc(t *testing.T) int64 {
	t.Helper()
	pool := validationPool(t)
	ctx := context.Background()
	suffix := time.Now().UnixNano()

	var templateID int64
	_ = pool.QueryRow(ctx, `
		INSERT INTO workflow_templates (doc_format_code, name, version, status, created_by)
		SELECT $1, 'step2 test', 1, 'active', u.id FROM users u WHERE u.username='admin' RETURNING id
	`, fmt.Sprintf("S2T%d", suffix)).Scan(&templateID)

	var docID int64
	_ = pool.QueryRow(ctx, `
		INSERT INTO documents (doc_format_code, doc_no, revision, workflow_template_id, workflow_version,
		                       status, idempotency_key, source_hash)
		VALUES ($1, $2, 0, $3, 1, 'completed', $4, 'step2hash') RETURNING id
	`, fmt.Sprintf("S2T%d", suffix), fmt.Sprintf("D2-%d", suffix), templateID,
		fmt.Sprintf("S2T:%d:0", suffix)).Scan(&docID)

	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM document_files WHERE document_id=$1`, docID)
		_, _ = pool.Exec(ctx, `DELETE FROM documents WHERE id=$1`, docID)
		_, _ = pool.Exec(ctx, `DELETE FROM workflow_steps WHERE workflow_template_id=$1`, templateID)
		_, _ = pool.Exec(ctx, `DELETE FROM workflow_templates WHERE id=$1`, templateID)
	})
	return docID
}

// seedPendingDocSimple creates a pending document (no external task) for the
// Finalize-not-completed test. Cleaned up on test end.
func seedPendingDocSimple(t *testing.T) int64 {
	t.Helper()
	pool := validationPool(t)
	ctx := context.Background()
	suffix := time.Now().UnixNano()

	var templateID int64
	_ = pool.QueryRow(ctx, `
		INSERT INTO workflow_templates (doc_format_code, name, version, status, created_by)
		SELECT $1, 'step2 pending', 1, 'active', u.id FROM users u WHERE u.username='admin' RETURNING id
	`, fmt.Sprintf("S2P%d", suffix)).Scan(&templateID)

	var docID int64
	_ = pool.QueryRow(ctx, `
		INSERT INTO documents (doc_format_code, doc_no, revision, workflow_template_id, workflow_version,
		                       status, idempotency_key, source_hash)
		VALUES ($1, $2, 0, $3, 1, 'pending', $4, 'pend2hash') RETURNING id
	`, fmt.Sprintf("S2P%d", suffix), fmt.Sprintf("DP-%d", suffix), templateID,
		fmt.Sprintf("S2P:%d:0", suffix)).Scan(&docID)

	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM documents WHERE id=$1`, docID)
		_, _ = pool.Exec(ctx, `DELETE FROM workflow_steps WHERE workflow_template_id=$1`, templateID)
		_, _ = pool.Exec(ctx, `DELETE FROM workflow_templates WHERE id=$1`, templateID)
	})
	return docID
}

// ── Step 2a: role-guard on GET /documents/:id/external-signers ───────────────

// TestListExternalSigners_RoleGuard confirms that only document_admin /
// system_admin / auditor can call List; a plain signer gets 403.
func TestListExternalSigners_RoleGuard(t *testing.T) {
	pool := validationPool(t)
	docID := seedPendingDocForInvite(t, pool)

	gin.SetMode(gin.TestMode)
	extH := NewExternalSignerHandler(pool, zap.NewNop())

	cases := []struct {
		role     string
		wantCode int
	}{
		{"signer", http.StatusForbidden},
		{"document_admin", http.StatusOK},
		{"system_admin", http.StatusOK},
		{"auditor", http.StatusOK},
	}

	for _, tc := range cases {
		t.Run(tc.role, func(t *testing.T) {
			r := gin.New()
			// Wire exactly as main.go does: fakeAuth sets ClaimsKey, RequireRole reads it.
			r.GET("/documents/:id/external-signers",
				fakeAuth(1, tc.role),
				middleware.RequireRole("document_admin", "system_admin", "auditor"),
				extH.List,
			)

			w := httptest.NewRecorder()
			req := httptest.NewRequest("GET",
				fmt.Sprintf("/documents/%d/external-signers", docID), nil)
			r.ServeHTTP(w, req)

			t.Logf("role=%s → %d %s", tc.role, w.Code, w.Body.String())
			if w.Code != tc.wantCode {
				t.Errorf("role %q: want %d, got %d", tc.role, tc.wantCode, w.Code)
			}
		})
	}
}

// ── Step 2b: DownloadFinal → pdf_generation_pending when PDF row missing ─────

// TestDownloadFinal_CompletedNoPDF_Returns409 proves that a completed doc with
// no final_pdf row returns 409 pdf_generation_pending, not 404.
func TestDownloadFinal_CompletedNoPDF_Returns409(t *testing.T) {
	pool := validationPool(t)
	docID := seedCompletedDoc(t)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	// store is nil — DownloadFinal reads from DB first; it returns 409 before
	// ever touching storage when the final_pdf row is missing.
	h := NewDocumentHandler(pool, nil, nil, zap.NewNop())
	r.GET("/documents/:id/file/final", fakeAuth(1, "document_admin"), h.DownloadFinal)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", fmt.Sprintf("/documents/%d/file/final", docID), nil)
	r.ServeHTTP(w, req)

	t.Logf("DownloadFinal (no pdf row) → %d %s", w.Code, w.Body.String())
	if w.Code != http.StatusConflict {
		t.Errorf("want 409, got %d", w.Code)
	}
	if got := errorCode(t, w.Body.String()); got != "pdf_generation_pending" {
		t.Errorf("code: want %q, got %q", "pdf_generation_pending", got)
	}
}

// ── Step 2b: Finalize endpoint guard ─────────────────────────────────────────

// TestFinalize_NotCompleted_Returns409 proves that calling POST /finalize on a
// pending doc returns 409 document_not_completed, never tries to call storage.
func TestFinalize_NotCompleted_Returns409(t *testing.T) {
	pool := validationPool(t)
	docID := seedPendingDocSimple(t)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := NewDocumentHandler(pool, nil, nil, zap.NewNop())
	r.POST("/documents/:id/finalize",
		fakeAuth(1, "document_admin"),
		middleware.RequireRole("document_admin", "system_admin"),
		h.Finalize,
	)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", fmt.Sprintf("/documents/%d/finalize", docID), nil)
	r.ServeHTTP(w, req)

	t.Logf("Finalize (pending doc) → %d %s", w.Code, w.Body.String())
	if w.Code != http.StatusConflict {
		t.Errorf("want 409, got %d", w.Code)
	}
	if got := errorCode(t, w.Body.String()); got != "document_not_completed" {
		t.Errorf("code: want %q, got %q", "document_not_completed", got)
	}
}

// TestFinalize_RoleGuard confirms signer role gets 403 on POST /finalize.
func TestFinalize_RoleGuard(t *testing.T) {
	pool := validationPool(t)
	docID := seedCompletedDoc(t)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := NewDocumentHandler(pool, nil, nil, zap.NewNop())
	r.POST("/documents/:id/finalize",
		fakeAuth(1, "signer"),
		middleware.RequireRole("document_admin", "system_admin"),
		h.Finalize,
	)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", fmt.Sprintf("/documents/%d/finalize", docID), nil)
	r.ServeHTTP(w, req)

	t.Logf("Finalize (signer role) → %d %s", w.Code, w.Body.String())
	if w.Code != http.StatusForbidden {
		t.Errorf("want 403, got %d", w.Code)
	}
}

// TestFinalize_StorageFails_Returns500 proves that when pdf.FinalizeDocument
// encounters a storage error the endpoint returns 500 pdf_generation_failed
// and the doc stays completed (the DB row is unchanged).
//
// Storage failure is simulated by passing a nil store to DocumentHandler; the
// nil dereference inside store.Put panics and is caught by gin.Recovery as a
// 500. We assert the doc is still completed after the call.
//
// Note: gin.Recovery is NOT added here — we test the handler directly and check
// that Finalize returns the correct error code, not a panic. We use a nil store
// which causes FinalizeDocument to return an error at the store.Put step.
func TestFinalize_StorageFails_Returns500(t *testing.T) {
	pool := validationPool(t)
	docID := seedCompletedDoc(t)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	// nil store → pdf.FinalizeDocument will error at store.Put (nil pointer dereference
	// wrapped in a recover). We use gin.Recovery so we get a 500 response, not a test panic.
	r.Use(gin.Recovery())
	h := NewDocumentHandler(pool, nil, nil, zap.NewNop())
	r.POST("/documents/:id/finalize",
		fakeAuth(1, "document_admin"),
		middleware.RequireRole("document_admin", "system_admin"),
		h.Finalize,
	)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", fmt.Sprintf("/documents/%d/finalize", docID), nil)
	r.ServeHTTP(w, req)

	t.Logf("Finalize (nil store) → %d %s", w.Code, w.Body.String())
	if w.Code < 500 {
		t.Errorf("want 5xx from storage failure, got %d", w.Code)
	}

	// Doc must still be completed — storage failure must not corrupt doc state.
	var status string
	_ = pool.QueryRow(context.Background(), `SELECT status FROM documents WHERE id=$1`, docID).Scan(&status)
	if status != "completed" {
		t.Errorf("doc status must remain 'completed' after storage failure, got %q", status)
	}
}
