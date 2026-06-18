package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"paperless-api/internal/auth"
	"paperless-api/internal/middleware"
)

const testSecret = "test-secret"

func init() {
	gin.SetMode(gin.TestMode)
}

func issueToken(t *testing.T, roles ...string) string {
	t.Helper()
	tok, err := auth.IssueAccessToken(testSecret, 1, "testuser", roles)
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}
	return tok
}

func TestRequireAuth_Missing(t *testing.T) {
	r := gin.New()
	r.GET("/protected", middleware.RequireAuth(testSecret), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/protected", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("got %d, want 401", w.Code)
	}
}

func TestRequireAuth_Valid(t *testing.T) {
	tok := issueToken(t, "signer")
	r := gin.New()
	r.GET("/protected", middleware.RequireAuth(testSecret), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("got %d, want 200", w.Code)
	}
}

func TestRequireRole_Allow(t *testing.T) {
	tok := issueToken(t, "system_admin")
	r := gin.New()
	r.GET("/admin",
		middleware.RequireAuth(testSecret),
		middleware.RequireRole("system_admin"),
		func(c *gin.Context) { c.Status(http.StatusOK) },
	)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/admin", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("got %d, want 200", w.Code)
	}
}

func TestRequireRole_Deny(t *testing.T) {
	tok := issueToken(t, "signer")
	r := gin.New()
	r.GET("/admin",
		middleware.RequireAuth(testSecret),
		middleware.RequireRole("system_admin"),
		func(c *gin.Context) { c.Status(http.StatusOK) },
	)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/admin", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("got %d, want 403", w.Code)
	}
}

// ── RequireAuthAllowQueryToken (PDF file routes) ─────────────────────────────
//
// These lock the audit-found behavior: the browser loads PDFs via <iframe>/<a>
// where it cannot set an Authorization header, so GET file routes must accept a
// ?token= query param. State-changing methods must NOT — a token in a URL leaks
// into logs/history, so it may never authorize a write.

func newQueryTokenRouter() *gin.Engine {
	r := gin.New()
	h := func(c *gin.Context) { c.Status(http.StatusOK) }
	r.GET("/file", middleware.RequireAuthAllowQueryToken(testSecret), h)
	r.POST("/file", middleware.RequireAuthAllowQueryToken(testSecret), h)
	return r
}

func TestRequireAuthAllowQueryToken_GET_QueryToken(t *testing.T) {
	tok := issueToken(t, "signer")
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/file?token="+tok, nil)
	newQueryTokenRouter().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("GET with ?token= must pass: got %d, want 200", w.Code)
	}
}

func TestRequireAuthAllowQueryToken_GET_HeaderStillWorks(t *testing.T) {
	tok := issueToken(t, "signer")
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/file", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	newQueryTokenRouter().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("GET with header must still pass: got %d, want 200", w.Code)
	}
}

func TestRequireAuthAllowQueryToken_GET_BadQueryToken(t *testing.T) {
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/file?token=garbage", nil)
	newQueryTokenRouter().ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("bad query token must 401: got %d, want 401", w.Code)
	}
}

func TestRequireAuthAllowQueryToken_GET_NoToken(t *testing.T) {
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/file", nil)
	newQueryTokenRouter().ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("no token must 401: got %d, want 401", w.Code)
	}
}

// SECURITY-critical: a POST must never be authorized by a query-string token,
// even a valid one. This prevents state-changing requests whose token leaks via
// access logs / browser history / Referer.
func TestRequireAuthAllowQueryToken_POST_QueryTokenRejected(t *testing.T) {
	tok := issueToken(t, "signer")
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/file?token="+tok, nil)
	newQueryTokenRouter().ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("POST with ?token= must be rejected: got %d, want 401 (URL-token write is a leak vector)", w.Code)
	}
}
