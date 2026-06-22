package handlers

import (
	"context"
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

	"paperless-api/internal/auth"
	"paperless-api/internal/middleware"
)

// validationPool is shared across all validation tests; gated on PAPERLESS_TEST_DB.
func validationPool(t *testing.T) *pgxpool.Pool {
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

// newInviteRouter builds a minimal router for the external-signers Invite endpoint.
// The route requires document_admin role — we inject a fake claims middleware.
func newInviteRouter(pool *pgxpool.Pool, role string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := NewExternalSignerHandler(pool, zap.NewNop())
	r.POST("/documents/:id/external-signers",
		fakeAuth(1, role),
		h.Invite,
	)
	return r
}

// fakeAuth injects fake JWT claims so handlers that call middleware.ClaimsFrom work.
func fakeAuth(userID int64, role string) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set(middleware.ClaimsKey, &auth.Claims{UserID: userID, Roles: []string{role}})
		c.Next()
	}
}

// seedPendingDocForInvite creates a pending doc with a waiting external task and
// returns the doc ID. Cleaned up on test end.
func seedPendingDocForInvite(t *testing.T, pool *pgxpool.Pool) int64 {
	t.Helper()
	ctx := context.Background()
	suffix := time.Now().UnixNano()

	var templateID, stepID int64
	_ = pool.QueryRow(ctx, `
		INSERT INTO workflow_templates (doc_format_code, name, version, status, created_by)
		SELECT $1, 'val test tmpl', 1, 'active', u.id FROM users u WHERE u.username='admin' RETURNING id
	`, fmt.Sprintf("VALT%d", suffix)).Scan(&templateID)
	_ = pool.QueryRow(ctx, `
		INSERT INTO workflow_steps (workflow_template_id, position_code, position_name, sequence_no, condition_type)
		VALUES ($1, 'CUSTOMER', 'ลูกค้า', 1, 3) RETURNING id
	`, templateID).Scan(&stepID)

	var docID int64
	_ = pool.QueryRow(ctx, `
		INSERT INTO documents (doc_format_code, doc_no, revision, workflow_template_id, workflow_version, status, idempotency_key, source_hash)
		VALUES ($1, $2, 0, $3, 1, 'pending', $4, 'valhash') RETURNING id
	`, fmt.Sprintf("VALT%d", suffix), fmt.Sprintf("D-%d", suffix), templateID,
		fmt.Sprintf("VALT:%d:0", suffix)).Scan(&docID)

	// Seed a waiting external task (no signer yet).
	_, _ = pool.Exec(ctx, `
		INSERT INTO signature_tasks (document_id, workflow_step_id, sequence_no, condition_type, status)
		VALUES ($1, $2, 1, 3, 'waiting')
	`, docID, stepID)

	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM signature_tasks WHERE document_id=$1`, docID)
		_, _ = pool.Exec(ctx, `DELETE FROM external_signers WHERE document_id=$1`, docID)
		_, _ = pool.Exec(ctx, `DELETE FROM documents WHERE id=$1`, docID)
		_, _ = pool.Exec(ctx, `DELETE FROM workflow_steps WHERE workflow_template_id=$1`, templateID)
		_, _ = pool.Exec(ctx, `DELETE FROM workflow_templates WHERE id=$1`, templateID)
	})
	return docID
}

// errorCode extracts the "code" field from a JSON error envelope.
func errorCode(t *testing.T, body string) string {
	t.Helper()
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal([]byte(body), &env)
	return env.Error.Code
}

// ── Invite validation (Step 1c) ───────────────────────────────────────────────

func TestInvite_ValidationErrors(t *testing.T) {
	pool := validationPool(t)
	docID := seedPendingDocForInvite(t, pool)
	r := newInviteRouter(pool, "document_admin")

	cases := []struct {
		name       string
		body       string
		wantStatus int
		wantCode   string
	}{
		{
			name:       "missing name",
			body:       `{}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_request",
		},
		{
			name:       "empty name after trim",
			body:       `{"name":"   "}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_request",
		},
		{
			name:       "name too long",
			body:       fmt.Sprintf(`{"name":%q}`, strings.Repeat("ก", 201)),
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_request",
		},
		{
			name:       "invalid email format",
			body:       `{"name":"Test","email":"notanemail"}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_request",
		},
		{
			name:       "email too long",
			body:       fmt.Sprintf(`{"name":"Test","email":"%s@example.com"}`, strings.Repeat("a", 250)),
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_request",
		},
		{
			name:       "phone too long",
			body:       fmt.Sprintf(`{"name":"Test","phone":%q}`, strings.Repeat("0", 31)),
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_request",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			req := httptest.NewRequest("POST",
				fmt.Sprintf("/documents/%d/external-signers", docID),
				strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			r.ServeHTTP(w, req)
			t.Logf("%s → %d %s", tc.name, w.Code, w.Body.String())
			if w.Code != tc.wantStatus {
				t.Errorf("status: want %d, got %d", tc.wantStatus, w.Code)
			}
			if got := errorCode(t, w.Body.String()); got != tc.wantCode {
				t.Errorf("code: want %q, got %q", tc.wantCode, got)
			}
		})
	}
}

func TestInvite_ExpiresInHours_Clamped(t *testing.T) {
	pool := validationPool(t)
	docID := seedPendingDocForInvite(t, pool)
	r := newInviteRouter(pool, "document_admin")

	// expires_in_hours above cap (168h) should be clamped, not rejected.
	w := httptest.NewRecorder()
	body := `{"name":"Test Signer","expires_in_hours":9999}`
	req := httptest.NewRequest("POST",
		fmt.Sprintf("/documents/%d/external-signers", docID),
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	t.Logf("oversized expiry → %d %s", w.Code, w.Body.String())
	// Must succeed (clamped to maxExpiryHours), not error.
	if w.Code != http.StatusCreated {
		t.Errorf("clamped expiry: want 201, got %d (%s)", w.Code, w.Body.String())
	}
	// Verify expires_at is at most maxExpiryHours from now.
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

// ── ExternalSign validation (Step 1c) ────────────────────────────────────────

func TestExternalSign_ValidationErrors(t *testing.T) {
	pool := validationPool(t)
	r, _ := newExtRouter(pool)

	// Valid-format but unmatched token: the signature_image / request_id checks
	// run before the DB token lookup, so these validations fire regardless of the
	// token matching a row.
	validFormatToken := strings.Repeat("ab", 32) // 64-char hex = valid format

	cases := []struct {
		name       string
		token      string
		body       string
		wantStatus int
		wantCode   string
	}{
		{
			name:       "missing signature_image",
			token:      validFormatToken,
			body:       `{"consent_text":"ok","request_id":"r1"}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   "signature_required",
		},
		{
			name:       "missing request_id",
			token:      validFormatToken,
			body:       `{"signature_image":"x"}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_request",
		},
		{
			name:       "oversized request_id",
			token:      validFormatToken,
			body:       fmt.Sprintf(`{"signature_image":"x","request_id":%q}`, strings.Repeat("x", 129)),
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_request",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			req := httptest.NewRequest("POST", "/external/sign", strings.NewReader(tc.body))
			req.Header.Set("X-Signer-Token", tc.token)
			req.Header.Set("Content-Type", "application/json")
			r.ServeHTTP(w, req)
			t.Logf("%s → %d %s", tc.name, w.Code, w.Body.String())
			if w.Code != tc.wantStatus {
				t.Errorf("status: want %d, got %d", tc.wantStatus, w.Code)
			}
			if got := errorCode(t, w.Body.String()); got != tc.wantCode {
				t.Errorf("code: want %q, got %q", tc.wantCode, got)
			}
		})
	}
}

// ── Import validation helpers (unit-level, no DB needed) ─────────────────────

func TestParseDate(t *testing.T) {
	cases := []struct {
		input   string
		wantErr bool
	}{
		{"2024-01-15", false},
		{"2000-02-29", false}, // leap year
		{"2023-02-29", true},  // not a leap year
		{"15-01-2024", true},
		{"2024/01/15", true},
		{"", true},
		{"2024-1-5", true},
		{"notadate", true},
	}
	for _, tc := range cases {
		_, err := parseDate(tc.input)
		if (err != nil) != tc.wantErr {
			t.Errorf("parseDate(%q): wantErr=%v, got err=%v", tc.input, tc.wantErr, err)
		}
	}
}

func TestValidateDecimal(t *testing.T) {
	cases := []struct {
		input   string
		wantErr bool
	}{
		{"1234.56", false},
		{"0", false},
		{"-99.99", false},
		{"1000000", false},
		{"", false},       // empty is allowed (optional field)
		{"abc", true},
		{"12.34.56", true},
		{"-", true},
		{"12-34", true},
		{"1e5", true}, // scientific notation not accepted
	}
	for _, tc := range cases {
		err := validateDecimal(tc.input)
		if (err != nil) != tc.wantErr {
			t.Errorf("validateDecimal(%q): wantErr=%v, got err=%v", tc.input, tc.wantErr, err)
		}
	}
}

