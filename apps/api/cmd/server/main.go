package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"paperless-api/internal/config"
	"paperless-api/internal/db"
	"paperless-api/internal/handlers"
	"paperless-api/internal/middleware"
)

func main() {
	cfg := config.Load()
	if err := cfg.Validate(); err != nil {
		log.Fatalf("config: %v", err)
	}

	logger, err := zap.NewProduction()
	if err != nil {
		log.Fatalf("init logger: %v", err)
	}
	defer logger.Sync()

	ctx := context.Background()
	pool, err := db.New(ctx, cfg)
	if err != nil {
		logger.Fatal("connect database", zap.Error(err))
	}
	defer pool.Close()

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(middleware.RequestID())
	r.Use(middleware.Logger(logger))

	// Health — no auth.
	h := handlers.NewHealthHandler(pool)
	r.GET("/health", h.Live)
	r.GET("/health/ready", h.Ready)

	// ── API v1 ──────────────────────────────────────────────────────────────
	// Feature routes (auth + RBAC) are registered here as handlers land.
	// See docs/api-contract.md for the planned surface.
	_ = r.Group("/api/v1")

	addr := cfg.Server.Host + ":" + cfg.Server.Port
	srv := &http.Server{
		Addr:         addr,
		Handler:      r,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	go func() {
		logger.Info("server starting", zap.String("addr", addr))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatal("server error", zap.Error(err))
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info("shutting down...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("shutdown error", zap.Error(err))
	}
}
