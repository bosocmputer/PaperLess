package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
)

// HealthHandler serves liveness and readiness checks.
type HealthHandler struct {
	pool *pgxpool.Pool
}

func NewHealthHandler(pool *pgxpool.Pool) *HealthHandler {
	return &HealthHandler{pool: pool}
}

// Live reports the process is up. No dependencies checked.
func (h *HealthHandler) Live(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// Ready reports the service can actually serve traffic: the PaperLess database
// is reachable. (Storage/SML reachability can be added as those clients land.)
func (h *HealthHandler) Ready(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()

	if err := h.pool.Ping(ctx); err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"status":   "error",
			"database": "error",
			"detail":   err.Error(),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok", "database": "ok"})
}
