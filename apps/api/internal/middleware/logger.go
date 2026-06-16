package middleware

import (
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// Logger emits one structured log line per request. It never logs request
// bodies, headers, tokens, or signatures — only safe metadata.
func Logger(log *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		log.Info("request",
			zap.String("request_id", c.GetString(RequestIDKey)),
			zap.String("method", c.Request.Method),
			zap.String("path", c.Request.URL.Path),
			zap.Int("status", c.Writer.Status()),
			zap.Duration("duration", time.Since(start)),
			zap.String("ip", c.ClientIP()),
		)
	}
}
