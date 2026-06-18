package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"paperless-api/internal/workflow"
)

// These tests probe the public external-sign HTTP surface against a real DB.
// Gated on PAPERLESS_TEST_DB. Storage is nil — the only route needing storage
// is the PDF download, which is not exercised here.

func auditPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("PAPERLESS_TEST_DB")
	if dsn == "" {
		t.Skip("PAPERLESS_TEST_DB not set")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// seedExternalDoc creates a single-external-step doc + signer + open task, and
// returns the raw token and the task id. Cleans up on test end.
func seedExternalDoc(t *testing.T, pool *pgxpool.Pool) (rawTokenHex string, docID, taskID int64) {
	t.Helper()
	ctx := context.Background()
	suffix := time.Now().UnixNano()

	var templateID, stepID int64
	_ = pool.QueryRow(ctx, `
		INSERT INTO workflow_templates (doc_format_code, name, version, status, created_by)
		SELECT $1, 'http audit', 1, 'active', u.id FROM users u WHERE u.username='admin' RETURNING id
	`, fmt.Sprintf("HAUD%d", suffix)).Scan(&templateID)
	_ = pool.QueryRow(ctx, `
		INSERT INTO workflow_steps (workflow_template_id, position_code, position_name, sequence_no, condition_type)
		VALUES ($1, 'CUSTOMER', 'ลูกค้า', 1, 3) RETURNING id
	`, templateID).Scan(&stepID)
	// A trailing internal step at seq=2 so external sign at seq=1 does NOT complete
	// the doc — this isolates sign/reuse logic from inline PDF finalization (which
	// needs storage). Completion-on-external-sign is covered by the engine test.
	var step2ID int64
	_ = pool.QueryRow(ctx, `
		INSERT INTO workflow_steps (workflow_template_id, position_code, position_name, sequence_no, condition_type)
		VALUES ($1, 'INTERNAL2', 'ภายใน', 2, 1) RETURNING id
	`, templateID).Scan(&step2ID)
	_, _ = pool.Exec(ctx, `
		INSERT INTO workflow_step_assignees (workflow_step_id, user_id, display_order)
		SELECT $1, u.id, 1 FROM users u WHERE u.username='maker'
	`, step2ID)

	_ = pool.QueryRow(ctx, `
		INSERT INTO documents (doc_format_code, doc_no, revision, workflow_template_id, workflow_version, status, idempotency_key, source_hash)
		VALUES ($1, $2, 0, $3, 1, 'pending', $4, 'h') RETURNING id
	`, fmt.Sprintf("HAUD%d", suffix), fmt.Sprintf("D-%d", suffix), templateID, fmt.Sprintf("HAUD:%d:0", suffix)).Scan(&docID)

	raw := make([]byte, 32)
	for i := range raw {
		raw[i] = byte((i * 7) % 251)
	}
	sum := sha256.Sum256(raw)
	tokenHash := hex.EncodeToString(sum[:])
	rawTokenHex = hex.EncodeToString(raw)

	var extSignerID int64
	_ = pool.QueryRow(ctx, `
		INSERT INTO external_signers (document_id, name, token_hash, token_expires_at, status)
		VALUES ($1, 'ลูกค้า HTTP', $2, now() + interval '72 hours', 'pending') RETURNING id
	`, docID, tokenHash).Scan(&extSignerID)

	_ = pool.QueryRow(ctx, `
		INSERT INTO signature_tasks (document_id, workflow_step_id, external_signer_id, sequence_no, condition_type, status, opened_at)
		VALUES ($1, $2, $3, 1, 3, 'open', now()) RETURNING id
	`, docID, stepID, extSignerID).Scan(&taskID)

	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM signature_events WHERE document_id=$1`, docID)
		_, _ = pool.Exec(ctx, `DELETE FROM audit_logs WHERE entity_type='document' AND entity_id=$1`, fmt.Sprintf("%d", docID))
		_, _ = pool.Exec(ctx, `DELETE FROM signature_tasks WHERE document_id=$1`, docID)
		_, _ = pool.Exec(ctx, `DELETE FROM external_signers WHERE document_id=$1`, docID)
		_, _ = pool.Exec(ctx, `DELETE FROM documents WHERE id=$1`, docID)
		_, _ = pool.Exec(ctx, `DELETE FROM workflow_steps WHERE workflow_template_id=$1`, templateID)
		_, _ = pool.Exec(ctx, `DELETE FROM workflow_templates WHERE id=$1`, templateID)
	})
	return rawTokenHex, docID, taskID
}

func newExtRouter(pool *pgxpool.Pool) (*gin.Engine, *ExternalSignHandler) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	eng := workflow.New(pool)
	h := NewExternalSignHandler(pool, nil, eng, zap.NewNop())
	r.GET("/external/document", h.DocumentView)
	r.POST("/external/sign", h.Sign)
	return r, h
}

func TestExtHTTP_GarbageToken_NoStackTrace(t *testing.T) {
	pool := auditPool(t)
	r, _ := newExtRouter(pool)

	cases := []struct{ name, token string }{
		{"empty", ""},
		{"not-hex", "zzzznothex!!!"},
		{"too-short", "00ff"},
		{"oversized", strings.Repeat("ab", 200)},
		{"valid-format-no-match", hex.EncodeToString(make([]byte, 32))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			req := httptest.NewRequest("GET", "/external/document", nil)
			if tc.token != "" {
				req.Header.Set("X-Signer-Token", tc.token)
			}
			r.ServeHTTP(w, req)
			t.Logf("%s → status %d body %s", tc.name, w.Code, w.Body.String())
			if w.Code >= 500 {
				t.Errorf("%s: got 5xx (%d) — must be a clean 4xx", tc.name, w.Code)
			}
		})
	}
}

func TestExtHTTP_ValidToken_View_Then_Sign_Then_Reuse(t *testing.T) {
	pool := auditPool(t)
	r, _ := newExtRouter(pool)
	rawToken, docID, _ := seedExternalDoc(t, pool)
	ctx := context.Background()

	// View with valid token.
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/external/document", nil)
	req.Header.Set("X-Signer-Token", rawToken)
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("view: want 200, got %d (%s)", w.Code, w.Body.String())
	}

	// Sign with valid token.
	body := `{"signature_image_hash":"sig123","consent_text":"ok","request_id":"http-req-1"}`
	w = httptest.NewRecorder()
	req = httptest.NewRequest("POST", "/external/sign", strings.NewReader(body))
	req.Header.Set("X-Signer-Token", rawToken)
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("sign: want 200, got %d (%s)", w.Code, w.Body.String())
	}

	// External task signed; trailing internal step keeps doc pending (by design).
	var extStatus string
	_ = pool.QueryRow(ctx, `SELECT status FROM external_signers WHERE document_id=$1`, docID).Scan(&extStatus)
	if extStatus != "signed" {
		t.Errorf("after external sign: external_signer status want signed, got %q", extStatus)
	}
	// The seq=2 internal task should now be open (sequence advanced).
	var seq2Open int
	_ = pool.QueryRow(ctx, `SELECT COUNT(*) FROM signature_tasks WHERE document_id=$1 AND sequence_no=2 AND status='open'`, docID).Scan(&seq2Open)
	if seq2Open != 1 {
		t.Errorf("seq=2 internal task should be open after external sign, got %d open", seq2Open)
	}

	// Reuse — must be rejected (Gone), not 200, not 500.
	w = httptest.NewRecorder()
	req = httptest.NewRequest("POST", "/external/sign", strings.NewReader(body))
	req.Header.Set("X-Signer-Token", rawToken)
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	t.Logf("reuse → status %d body %s", w.Code, w.Body.String())
	if w.Code == 200 {
		t.Error("REUSE VIOLATION: second sign returned 200")
	}
	if w.Code >= 500 {
		t.Errorf("reuse: got 5xx (%d), want clean 4xx", w.Code)
	}

	// Exactly one event.
	var events int
	_ = pool.QueryRow(ctx, `SELECT COUNT(*) FROM signature_events WHERE document_id=$1`, docID).Scan(&events)
	if events != 1 {
		t.Errorf("want exactly 1 signature_event, got %d", events)
	}

	// View after sign — must report used, not 200.
	w = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/external/document", nil)
	req.Header.Set("X-Signer-Token", rawToken)
	r.ServeHTTP(w, req)
	var resp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	t.Logf("view-after-sign → status %d body %s", w.Code, w.Body.String())
	if w.Code == 200 {
		t.Error("view after sign returned 200 — should report external_link_used")
	}
}

func TestExtHTTP_RateLimit_Triggers(t *testing.T) {
	pool := auditPool(t)
	r, _ := newExtRouter(pool)

	garbage := hex.EncodeToString(make([]byte, 32))
	var got429 bool
	for i := 0; i < 30; i++ {
		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/external/document", nil)
		req.Header.Set("X-Signer-Token", garbage)
		// Same client IP for all (httptest default RemoteAddr).
		r.ServeHTTP(w, req)
		if w.Code == http.StatusTooManyRequests {
			got429 = true
			t.Logf("rate limit triggered at attempt %d", i+1)
			break
		}
	}
	if !got429 {
		t.Error("rate limit never triggered after 30 rapid requests from same IP")
	}
}
