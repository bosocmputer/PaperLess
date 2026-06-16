package middleware

import (
	"crypto/rand"
	"encoding/hex"

	"github.com/gin-gonic/gin"
)

const RequestIDKey = "request_id"

// RequestID reads X-Request-ID from the client or generates a random one,
// stores it in the context, and echoes it back in the response header.
// Mirrors the sml-api-bybos convention so request ids correlate across services.
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader("X-Request-ID")
		if id == "" {
			b := make([]byte, 8)
			_, _ = rand.Read(b)
			id = hex.EncodeToString(b)
		}
		c.Set(RequestIDKey, id)
		c.Header("X-Request-ID", id)
		c.Next()
	}
}
