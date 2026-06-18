package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"paperless-api/internal/auth"
	"paperless-api/internal/httpx"
)

const ClaimsKey = "auth_claims"

// RequireAuth validates the Bearer JWT and stores the claims in the context.
func RequireAuth(jwtSecret string) gin.HandlerFunc {
	return authMiddleware(jwtSecret, false)
}

// RequireAuthAllowQueryToken behaves like RequireAuth but, for GET requests only,
// also accepts the token via a ?token= query parameter when no Authorization
// header is present. This is required for browser-driven file viewing where the
// token cannot be set as a header (<iframe src>, <a href> download).
//
// SECURITY: query tokens can leak into access logs, browser history, and the
// Referer header. It is therefore restricted to (a) GET only — never a
// state-changing method — and (b) wired onto the read-only file routes only, not
// the whole API. The Authorization header remains the preferred path and takes
// precedence when both are present.
func RequireAuthAllowQueryToken(jwtSecret string) gin.HandlerFunc {
	return authMiddleware(jwtSecret, true)
}

func authMiddleware(jwtSecret string, allowQueryToken bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		var tokenStr string
		header := c.GetHeader("Authorization")
		if strings.HasPrefix(header, "Bearer ") {
			tokenStr = strings.TrimPrefix(header, "Bearer ")
		} else if allowQueryToken && c.Request.Method == http.MethodGet {
			tokenStr = c.Query("token")
		}
		if tokenStr == "" {
			httpx.Error(c, http.StatusUnauthorized, "unauthorized", "missing or invalid authorization header")
			c.Abort()
			return
		}
		claims, err := auth.ParseAccessToken(jwtSecret, tokenStr)
		if err != nil {
			httpx.Error(c, http.StatusUnauthorized, "unauthorized", "invalid or expired token")
			c.Abort()
			return
		}
		c.Set(ClaimsKey, claims)
		c.Next()
	}
}

// RequireRole aborts with 403 if the authenticated user does not hold at least
// one of the listed role codes.
func RequireRole(roles ...string) gin.HandlerFunc {
	required := make(map[string]struct{}, len(roles))
	for _, r := range roles {
		required[r] = struct{}{}
	}
	return func(c *gin.Context) {
		claimsAny, ok := c.Get(ClaimsKey)
		if !ok {
			httpx.Error(c, http.StatusForbidden, "forbidden", "not authenticated")
			c.Abort()
			return
		}
		claims := claimsAny.(*auth.Claims)
		for _, r := range claims.Roles {
			if _, has := required[r]; has {
				c.Next()
				return
			}
		}
		httpx.Error(c, http.StatusForbidden, "forbidden", "insufficient role")
		c.Abort()
	}
}

// ClaimsFrom retrieves auth claims stored by RequireAuth.
func ClaimsFrom(c *gin.Context) *auth.Claims {
	v, _ := c.Get(ClaimsKey)
	claims, _ := v.(*auth.Claims)
	return claims
}
