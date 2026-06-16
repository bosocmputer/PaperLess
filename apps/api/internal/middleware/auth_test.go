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
