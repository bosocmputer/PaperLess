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
	return func(c *gin.Context) {
		header := c.GetHeader("Authorization")
		if !strings.HasPrefix(header, "Bearer ") {
			httpx.Error(c, http.StatusUnauthorized, "unauthorized", "missing or invalid authorization header")
			c.Abort()
			return
		}
		tokenStr := strings.TrimPrefix(header, "Bearer ")
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
