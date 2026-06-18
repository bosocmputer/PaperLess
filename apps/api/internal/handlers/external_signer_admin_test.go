package handlers

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"paperless-api/internal/middleware"
	"paperless-api/internal/workflow"
)

// ── helpers ──────────────────────────────────────────────────────────────────

// seedSignerWithTask creates a pending external signer + open c3 task for the given doc.
// Returns the signer id and the raw 64-char hex token (so tests can probe the old token).
func seedSignerWithTask(t *testing.T, pool *pgxpool.Pool, docID int64) (signerID int64, rawToken string) {
	t.Helper()
	ctx := context.Background()
	suffix := time.Now().UnixNano()

	// Create a workflow step for the doc's template.
	var tmplID int64
	if err := pool.QueryRow(ctx,
		`SELECT workflow_template_id FROM documents WHERE id=$1`, docID,
	).Scan(&tmplID); err != nil {
		t.Fatalf("resolve template: %v", err)
	}

	var stepID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO workflow_steps (workflow_template_id, position_code, position_name, sequence_no, condition_type)
		VALUES ($1, $2, 'ลูกค้า', 1, 3) RETURNING id
	`, tmplID, fmt.Sprintf("CUST%d", suffix)).Scan(&stepID); err != nil {
		t.Fatalf("seed step: %v", err)
	}

	// Generate token (mirrors Invite exactly).
	rawBytes := make([]byte, 32)
	if _, err := rand.Read(rawBytes); err != nil {
		t.Fatalf("generate token: %v", err)
	}
	rawToken = hex.EncodeToString(rawBytes)
	h := sha256.Sum256(rawBytes)
	tokenHash := hex.EncodeToString(h[:])

	expiresAt := time.Now().UTC().Add(72 * time.Hour)

	if err := pool.QueryRow(ctx, `
		INSERT INTO external_signers (document_id, name, email, token_hash, token_expires_at, status)
		VALUES ($1, 'Test Signer', 'test@example.com', $2, $3, 'pending')
		RETURNING id
	`, docID, tokenHash, expiresAt).Scan(&signerID); err != nil {
		t.Fatalf("seed signer: %v", err)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO signature_tasks (document_id, workflow_step_id, sequence_no, condition_type, status, external_signer_id, opened_at)
		VALUES ($1, $2, 1, 3, 'open', $3, now())
	`, docID, stepID, signerID); err != nil {
		t.Fatalf("seed task: %v", err)
	}

	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM audit_logs WHERE entity_type='external_signer' AND entity_id=$1`,
			fmt.Sprintf("%d", signerID))
		_, _ = pool.Exec(ctx, `DELETE FROM signature_tasks WHERE document_id=$1 AND external_signer_id=$2`, docID, signerID)
		_, _ = pool.Exec(ctx, `DELETE FROM signature_tasks WHERE document_id=$1 AND workflow_step_id=$2`, docID, stepID)
		_, _ = pool.Exec(ctx, `DELETE FROM external_signers WHERE id=$1`, signerID)
		_, _ = pool.Exec(ctx, `DELETE FROM workflow_steps WHERE id=$1`, stepID)
	})
	return signerID, rawToken
}

// seedDocForAdminActions seeds a pending doc with no steps attached to the template yet.
func seedDocForAdminActions(t *testing.T, pool *pgxpool.Pool) int64 {
	t.Helper()
	ctx := context.Background()
	suffix := time.Now().UnixNano()
	fmtCode := fmt.Sprintf("ADM%d", suffix)

	var tmplID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO workflow_templates (doc_format_code, name, version, status, created_by)
		SELECT $1, 'admin action test', 1, 'active', u.id FROM users u WHERE u.username='admin' RETURNING id
	`, fmtCode).Scan(&tmplID); err != nil {
		t.Fatalf("seed template: %v", err)
	}

	var docID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO documents (doc_format_code, doc_no, revision, workflow_template_id, workflow_version,
		                       status, idempotency_key, source_hash)
		VALUES ($1, $2, 0, $3, 1, 'pending', $4, 'admhash') RETURNING id
	`, fmtCode, fmt.Sprintf("ADM-001-%d", suffix), tmplID,
		fmt.Sprintf("ADM:%d:0", suffix)).Scan(&docID); err != nil {
		t.Fatalf("seed doc: %v", err)
	}

	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM documents WHERE id=$1`, docID)
		_, _ = pool.Exec(ctx, `DELETE FROM workflow_templates WHERE id=$1`, tmplID)
	})
	return docID
}

// newAdminSignerRouter builds a minimal router for the cancel/resend admin endpoints.
// RequireRole is included to mirror the real server's route-level guard.
func newAdminSignerRouter(pool *pgxpool.Pool, role string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(gin.Recovery())
	h := NewExternalSignerHandler(pool, zap.NewNop())
	adminOnly := middleware.RequireRole("document_admin", "system_admin")
	r.POST("/documents/:id/external-signers/:signerId/cancel",
		fakeAuth(1, role), adminOnly, h.Cancel)
	r.POST("/documents/:id/external-signers/:signerId/resend",
		fakeAuth(1, role), adminOnly, h.Resend)
	return r
}

// newExtViewRouter builds a minimal router to probe the old token via DocumentView.
func newExtViewRouter(pool *pgxpool.Pool) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	wfEngine := workflow.New(pool)
	h := NewExternalSignHandler(pool, nil, wfEngine, zap.NewNop())
	r.GET("/external/document", h.DocumentView)
	return r
}

// probeToken sends GET /external/document with the given raw token and returns the HTTP status.
func probeToken(r *gin.Engine, rawToken string) int {
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/external/document", nil)
	req.Header.Set("X-Signer-Token", rawToken)
	r.ServeHTTP(w, req)
	return w.Code
}

// taskStatusForDoc returns the status of the most recent task for the given doc+signerID.
func taskStatusForDoc(t *testing.T, pool *pgxpool.Pool, docID, signerID int64) string {
	t.Helper()
	var status string
	err := pool.QueryRow(context.Background(),
		`SELECT status FROM signature_tasks WHERE document_id=$1 AND external_signer_id=$2 LIMIT 1`,
		docID, signerID,
	).Scan(&status)
	if err != nil {
		t.Fatalf("fetch task status: %v", err)
	}
	return status
}

// waitingTaskExists returns true when a waiting unlinked c3 task exists for the doc.
func waitingTaskExists(t *testing.T, pool *pgxpool.Pool, docID int64) bool {
	t.Helper()
	var count int
	_ = pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM signature_tasks WHERE document_id=$1 AND status='waiting' AND external_signer_id IS NULL`,
		docID,
	).Scan(&count)
	return count > 0
}

// tokenHashInDB returns the token_hash stored for the given signer id.
func tokenHashInDB(t *testing.T, pool *pgxpool.Pool, signerID int64) string {
	t.Helper()
	var hash string
	if err := pool.QueryRow(context.Background(),
		`SELECT token_hash FROM external_signers WHERE id=$1`, signerID,
	).Scan(&hash); err != nil {
		t.Fatalf("fetch token_hash: %v", err)
	}
	return hash
}

// ── Role guard ────────────────────────────────────────────────────────────────

func TestAdminSigner_RoleGuard(t *testing.T) {
	pool := validationPool(t)
	docID := seedDocForAdminActions(t, pool)
	signerID, _ := seedSignerWithTask(t, pool, docID)

	// Only document_admin and system_admin may call cancel/resend.
	deniedRoles := []string{"auditor", "signer", "workflow_admin"}

	for _, role := range deniedRoles {
		t.Run("cancel/"+role, func(t *testing.T) {
			r := newAdminSignerRouter(pool, role)
			w := httptest.NewRecorder()
			req := httptest.NewRequest("POST",
				fmt.Sprintf("/documents/%d/external-signers/%d/cancel", docID, signerID), nil)
			r.ServeHTTP(w, req)
			t.Logf("cancel %s → %d", role, w.Code)
			if w.Code != http.StatusForbidden {
				t.Errorf("role %s: want 403, got %d", role, w.Code)
			}
		})
		t.Run("resend/"+role, func(t *testing.T) {
			r := newAdminSignerRouter(pool, role)
			w := httptest.NewRecorder()
			req := httptest.NewRequest("POST",
				fmt.Sprintf("/documents/%d/external-signers/%d/resend", docID, signerID), nil)
			r.ServeHTTP(w, req)
			t.Logf("resend %s → %d", role, w.Code)
			if w.Code != http.StatusForbidden {
				t.Errorf("role %s: want 403, got %d", role, w.Code)
			}
		})
	}

	allowedRoles := []string{"document_admin", "system_admin"}
	for _, role := range allowedRoles {
		t.Run("cancel-allowed/"+role, func(t *testing.T) {
			r := newAdminSignerRouter(pool, role)
			w := httptest.NewRecorder()
			req := httptest.NewRequest("POST",
				fmt.Sprintf("/documents/%d/external-signers/%d/cancel", docID, signerID), nil)
			r.ServeHTTP(w, req)
			t.Logf("cancel %s → %d", role, w.Code)
			// First call cancels; subsequent calls are idempotent (both 200).
			if w.Code != http.StatusOK {
				t.Errorf("role %s: want 200, got %d: %s", role, w.Code, w.Body.String())
			}
		})
	}
}

// ── Cancel ───────────────────────────────────────────────────────────────────

// TestCancel_TaskReturnsToWaiting is the core behaviour test: after cancel, the
// linked task must be 'waiting' with external_signer_id = NULL so a resend can
// activate it again.
func TestCancel_TaskReturnsToWaiting(t *testing.T) {
	pool := validationPool(t)
	docID := seedDocForAdminActions(t, pool)
	signerID, _ := seedSignerWithTask(t, pool, docID)

	r := newAdminSignerRouter(pool, "document_admin")

	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST",
		fmt.Sprintf("/documents/%d/external-signers/%d/cancel", docID, signerID), nil)
	r.ServeHTTP(w, req)
	t.Logf("cancel → %d %s", w.Code, w.Body.String())

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}

	// Signer status must be 'cancelled'.
	var signerStatus string
	_ = pool.QueryRow(context.Background(),
		`SELECT status FROM external_signers WHERE id=$1`, signerID,
	).Scan(&signerStatus)
	if signerStatus != "cancelled" {
		t.Errorf("signer status: want cancelled, got %q", signerStatus)
	}

	// Task must be 'waiting' with no signer link.
	if !waitingTaskExists(t, pool, docID) {
		t.Error("expected a waiting unlinked c3 task after cancel")
	}
}

// TestCancel_Idempotent confirms that cancelling an already-cancelled signer
// returns 200, never 500 or 409.
func TestCancel_Idempotent(t *testing.T) {
	pool := validationPool(t)
	docID := seedDocForAdminActions(t, pool)
	signerID, _ := seedSignerWithTask(t, pool, docID)

	r := newAdminSignerRouter(pool, "document_admin")
	url := fmt.Sprintf("/documents/%d/external-signers/%d/cancel", docID, signerID)

	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("POST", url, nil))
		t.Logf("cancel attempt %d → %d %s", i+1, w.Code, w.Body.String())
		if w.Code != http.StatusOK {
			t.Errorf("attempt %d: want 200, got %d", i+1, w.Code)
		}
	}
}

// TestCancel_NotFound confirms 404 when signer or doc does not exist.
func TestCancel_NotFound(t *testing.T) {
	pool := validationPool(t)
	r := newAdminSignerRouter(pool, "document_admin")

	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/documents/999999999/external-signers/999999999/cancel", nil)
	r.ServeHTTP(w, req)
	t.Logf("cancel nonexistent → %d %s", w.Code, w.Body.String())
	if w.Code != http.StatusNotFound {
		t.Errorf("want 404, got %d", w.Code)
	}
}

// TestCancel_TerminalDoc confirms 409 when the document is completed/rejected/cancelled.
func TestCancel_TerminalDoc(t *testing.T) {
	pool := validationPool(t)
	ctx := context.Background()
	docID := seedDocForAdminActions(t, pool)
	signerID, _ := seedSignerWithTask(t, pool, docID)

	// Mark doc completed.
	_, _ = pool.Exec(ctx, `UPDATE documents SET status='completed' WHERE id=$1`, docID)
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `UPDATE documents SET status='pending' WHERE id=$1`, docID)
	})

	r := newAdminSignerRouter(pool, "document_admin")
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST",
		fmt.Sprintf("/documents/%d/external-signers/%d/cancel", docID, signerID), nil)
	r.ServeHTTP(w, req)
	t.Logf("cancel terminal doc → %d %s", w.Code, w.Body.String())
	if w.Code != http.StatusConflict {
		t.Errorf("want 409, got %d", w.Code)
	}
	if got := errorCode(t, w.Body.String()); got != "document_terminal" {
		t.Errorf("code: want document_terminal, got %q", got)
	}
}

// TestCancel_AuditWritten confirms that cancel writes an audit_logs row with
// action='external_signer_cancelled'.
func TestCancel_AuditWritten(t *testing.T) {
	pool := validationPool(t)
	docID := seedDocForAdminActions(t, pool)
	signerID, _ := seedSignerWithTask(t, pool, docID)

	r := newAdminSignerRouter(pool, "document_admin")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("POST",
		fmt.Sprintf("/documents/%d/external-signers/%d/cancel", docID, signerID), nil))
	if w.Code != http.StatusOK {
		t.Fatalf("cancel failed: %d %s", w.Code, w.Body.String())
	}

	var count int
	_ = pool.QueryRow(context.Background(), `
		SELECT COUNT(*) FROM audit_logs
		 WHERE action='external_signer_cancelled'
		   AND entity_type='external_signer'
		   AND entity_id=$1
	`, fmt.Sprintf("%d", signerID)).Scan(&count)
	if count == 0 {
		t.Error("expected audit_logs row for external_signer_cancelled")
	}
}

// ── Resend ───────────────────────────────────────────────────────────────────

// TestResend_FreshTokenIssued is the core resend behaviour test:
//   - a fresh token is returned in the response
//   - the new token hash is stored in the DB (the old hash is gone)
//   - the raw token is NOT the old one
//   - the raw token is NOT stored in the DB (only the hash)
func TestResend_FreshTokenIssued(t *testing.T) {
	pool := validationPool(t)
	docID := seedDocForAdminActions(t, pool)
	signerID, oldRawToken := seedSignerWithTask(t, pool, docID)

	oldHash := tokenHashInDB(t, pool, signerID)

	r := newAdminSignerRouter(pool, "document_admin")
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST",
		fmt.Sprintf("/documents/%d/external-signers/%d/resend", docID, signerID),
		strings.NewReader(`{"expires_in_hours": 48}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	t.Logf("resend → %d %s", w.Code, w.Body.String())

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Data struct {
			Token     string `json:"token"`
			ExpiresAt string `json:"expires_at"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	newRawToken := resp.Data.Token

	// Response must contain a new 64-char hex token.
	if len(newRawToken) != 64 {
		t.Errorf("token length: want 64, got %d", len(newRawToken))
	}
	// Must differ from the old token.
	if newRawToken == oldRawToken {
		t.Error("resend returned the same token as before")
	}

	// DB must store the NEW hash, not the old one.
	newHash := tokenHashInDB(t, pool, signerID)
	if newHash == oldHash {
		t.Error("DB token_hash unchanged after resend")
	}

	// The raw token must NOT be stored in the DB.
	var rawCount int
	_ = pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM external_signers WHERE id=$1 AND token_hash=$2`,
		signerID, newRawToken,
	).Scan(&rawCount)
	if rawCount > 0 {
		t.Error("raw token must never be stored in the DB — only the hash")
	}

	// The new hash must match SHA-256 of the returned token.
	rawBytes, err := hex.DecodeString(newRawToken)
	if err != nil {
		t.Fatalf("decode new token: %v", err)
	}
	h := sha256.Sum256(rawBytes)
	expectedHash := hex.EncodeToString(h[:])
	if newHash != expectedHash {
		t.Errorf("DB hash does not match SHA-256 of returned token")
	}
}

// TestResend_OldTokenInvalidated proves that after resend, the old token returns
// 401 from the external DocumentView endpoint. This closes the "old link still
// works" risk.
func TestResend_OldTokenInvalidated(t *testing.T) {
	pool := validationPool(t)
	docID := seedDocForAdminActions(t, pool)
	signerID, oldRawToken := seedSignerWithTask(t, pool, docID)

	extRouter := newExtViewRouter(pool)
	adminRouter := newAdminSignerRouter(pool, "document_admin")

	// Old token must be valid before resend.
	if code := probeToken(extRouter, oldRawToken); code != http.StatusOK {
		t.Fatalf("old token before resend: want 200, got %d", code)
	}

	// Resend.
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST",
		fmt.Sprintf("/documents/%d/external-signers/%d/resend", docID, signerID), nil)
	adminRouter.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("resend failed: %d %s", w.Code, w.Body.String())
	}

	// Old token must now be rejected.
	if code := probeToken(extRouter, oldRawToken); code == http.StatusOK {
		t.Error("old token still valid after resend — hash was not overwritten")
	}
	t.Logf("old token after resend → %d (expected non-200)", probeToken(extRouter, oldRawToken))
}

// TestResend_OnlyPending confirms that resend on a signed or cancelled signer
// returns 409.
func TestResend_OnlyPending(t *testing.T) {
	pool := validationPool(t)
	ctx := context.Background()

	cases := []struct {
		name   string
		status string
	}{
		{"signed", "signed"},
		{"cancelled", "cancelled"},
		{"expired", "expired"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			docID := seedDocForAdminActions(t, pool)
			signerID, _ := seedSignerWithTask(t, pool, docID)

			// Force signer to the target status.
			_, _ = pool.Exec(ctx, `UPDATE external_signers SET status=$1 WHERE id=$2`, tc.status, signerID)

			r := newAdminSignerRouter(pool, "document_admin")
			w := httptest.NewRecorder()
			req := httptest.NewRequest("POST",
				fmt.Sprintf("/documents/%d/external-signers/%d/resend", docID, signerID), nil)
			r.ServeHTTP(w, req)
			t.Logf("resend %s signer → %d %s", tc.status, w.Code, w.Body.String())

			if w.Code != http.StatusConflict {
				t.Errorf("signer %s: want 409, got %d", tc.status, w.Code)
			}
			if got := errorCode(t, w.Body.String()); got != "signer_not_resendable" {
				t.Errorf("code: want signer_not_resendable, got %q", got)
			}
		})
	}
}

// TestResend_TerminalDoc confirms 409 when the document is in a terminal state.
func TestResend_TerminalDoc(t *testing.T) {
	pool := validationPool(t)
	ctx := context.Background()

	for _, docStatus := range []string{"completed", "rejected", "cancelled"} {
		t.Run(docStatus, func(t *testing.T) {
			docID := seedDocForAdminActions(t, pool)
			signerID, _ := seedSignerWithTask(t, pool, docID)

			_, _ = pool.Exec(ctx, `UPDATE documents SET status=$1 WHERE id=$2`, docStatus, docID)
			t.Cleanup(func() {
				_, _ = pool.Exec(ctx, `UPDATE documents SET status='pending' WHERE id=$1`, docID)
			})

			r := newAdminSignerRouter(pool, "document_admin")
			w := httptest.NewRecorder()
			req := httptest.NewRequest("POST",
				fmt.Sprintf("/documents/%d/external-signers/%d/resend", docID, signerID), nil)
			r.ServeHTTP(w, req)
			t.Logf("resend on %s doc → %d %s", docStatus, w.Code, w.Body.String())

			if w.Code != http.StatusConflict {
				t.Errorf("doc %s: want 409, got %d", docStatus, w.Code)
			}
			if got := errorCode(t, w.Body.String()); got != "document_terminal" {
				t.Errorf("code: want document_terminal, got %q", got)
			}
		})
	}
}

// TestResend_AuditWritten confirms that resend writes an audit_logs row with
// action='external_signer_resent'.
func TestResend_AuditWritten(t *testing.T) {
	pool := validationPool(t)
	docID := seedDocForAdminActions(t, pool)
	signerID, _ := seedSignerWithTask(t, pool, docID)

	r := newAdminSignerRouter(pool, "document_admin")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("POST",
		fmt.Sprintf("/documents/%d/external-signers/%d/resend", docID, signerID), nil))
	if w.Code != http.StatusOK {
		t.Fatalf("resend failed: %d %s", w.Code, w.Body.String())
	}

	var count int
	_ = pool.QueryRow(context.Background(), `
		SELECT COUNT(*) FROM audit_logs
		 WHERE action='external_signer_resent'
		   AND entity_type='external_signer'
		   AND entity_id=$1
	`, fmt.Sprintf("%d", signerID)).Scan(&count)
	if count == 0 {
		t.Error("expected audit_logs row for external_signer_resent")
	}
}

// TestResend_ExpiryClamp confirms that expires_in_hours above maxExpiryHours is
// clamped, not rejected (mirrors Invite behaviour).
func TestResend_ExpiryClamp(t *testing.T) {
	pool := validationPool(t)
	docID := seedDocForAdminActions(t, pool)
	_, _ = seedSignerWithTask(t, pool, docID)

	// Get the signerID we just created.
	var signerID int64
	_ = pool.QueryRow(context.Background(),
		`SELECT id FROM external_signers WHERE document_id=$1 ORDER BY created_at DESC LIMIT 1`, docID,
	).Scan(&signerID)

	r := newAdminSignerRouter(pool, "document_admin")
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST",
		fmt.Sprintf("/documents/%d/external-signers/%d/resend", docID, signerID),
		strings.NewReader(`{"expires_in_hours": 9999}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	t.Logf("resend oversized expiry → %d %s", w.Code, w.Body.String())
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Data struct {
			ExpiresAt string `json:"expires_at"`
		} `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Data.ExpiresAt != "" {
		exp, err := time.Parse(time.RFC3339, resp.Data.ExpiresAt)
		if err == nil {
			hours := time.Until(exp).Hours()
			if hours > float64(maxExpiryHours)+1 {
				t.Errorf("expires_at not clamped: %.1f hours from now (want ≤%d)", hours, maxExpiryHours)
			}
		}
	}
}
