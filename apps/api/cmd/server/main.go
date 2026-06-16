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
	"paperless-api/internal/storage"
	"paperless-api/internal/workflow"
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

	store, err := storage.New(cfg)
	if err != nil {
		logger.Fatal("connect minio", zap.Error(err))
	}
	if err := store.EnsureBucket(ctx); err != nil {
		logger.Warn("minio bucket init", zap.Error(err))
	}

	wfEngine := workflow.New(pool)

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
	v1 := r.Group("/api/v1")

	// Auth (public)
	authH := handlers.NewAuthHandler(pool, cfg.Auth.JWTSecret, logger)
	authG := v1.Group("/auth")
	{
		authG.POST("/login", authH.Login)
		authG.POST("/refresh", authH.Refresh)
		authG.POST("/logout", authH.Logout)
		authG.GET("/me", middleware.RequireAuth(cfg.Auth.JWTSecret), authH.Me)
	}

	// Documents (auth required)
	requireAuth := middleware.RequireAuth(cfg.Auth.JWTSecret)
	docH := handlers.NewDocumentHandler(pool, store, wfEngine, logger)
	attachH := handlers.NewAttachmentHandler(pool, store, logger)
	auditH := handlers.NewAuditHandler(pool, wfEngine)
	docsG := v1.Group("/documents", requireAuth)
	{
		docsG.POST("/import", docH.Import)
		docsG.GET("/:id", docH.Get)
		docsG.GET("/:id/file/original", docH.DownloadOriginal)
		docsG.POST("/:id/attachments", attachH.Upload)
		docsG.GET("/:id/attachments", attachH.List)
		docsG.GET("/:id/audit-logs", auditH.AuditLogs)
		docsG.GET("/:id/workflow-status", auditH.WorkflowStatus)
	}

	// Standalone attachment delete (by file id, not doc id)
	v1.DELETE("/attachments/:id", requireAuth, attachH.Delete)

	// Signature tasks (auth required)
	taskH := handlers.NewTaskHandler(pool, store, wfEngine, logger)
	tasksG := v1.Group("/signature-tasks", requireAuth)
	{
		tasksG.GET("/inbox", taskH.Inbox)
		tasksG.GET("/:id", taskH.GetTask)
		tasksG.POST("/:id/sign", taskH.Sign)
		tasksG.POST("/:id/reject", taskH.Reject)
	}

	// Final PDF download (auth required; document must be completed)
	docsG.GET("/:id/file/final", docH.DownloadFinal)

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
