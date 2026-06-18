package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"paperless-api/internal/storage"
)

// HealthHandler serves liveness and readiness checks.
type HealthHandler struct {
	pool  *pgxpool.Pool
	store *storage.Client // nil disables the MinIO check (tests, dev without MinIO)
}

func NewHealthHandler(pool *pgxpool.Pool, store *storage.Client) *HealthHandler {
	return &HealthHandler{pool: pool, store: store}
}

// Live reports the process is up. No dependencies checked.
func (h *HealthHandler) Live(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// Ready reports the service can actually serve traffic: both the PaperLess
// database and MinIO storage must be reachable. A pilot operator relying on
// this endpoint to gate deploys needs to know if storage is down.
func (h *HealthHandler) Ready(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()

	resp := gin.H{}
	healthy := true

	if err := h.pool.Ping(ctx); err != nil {
		resp["database"] = "error"
		resp["database_detail"] = err.Error()
		healthy = false
	} else {
		resp["database"] = "ok"
	}

	if h.store != nil {
		if err := h.store.Ping(ctx); err != nil {
			resp["storage"] = "error"
			resp["storage_detail"] = err.Error()
			healthy = false
		} else {
			resp["storage"] = "ok"
		}
	}

	if !healthy {
		resp["status"] = "error"
		c.JSON(http.StatusServiceUnavailable, resp)
		return
	}
	resp["status"] = "ok"
	c.JSON(http.StatusOK, resp)
}
