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
	"paperless-api/internal/sml"
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

	// SML sync worker — only started when SML is configured.
	// During pilot (no SML_API_KEY), jobs queue as 'pending' harmlessly.
	workerCtx, workerCancel := context.WithCancel(ctx)
	defer workerCancel()
	if smlClient := sml.NewClient(cfg); smlClient != nil {
		smlWorker := sml.NewWorker(pool, smlClient, logger, 5*time.Second)
		go smlWorker.Run(workerCtx)
		logger.Info("sml sync worker started", zap.String("base_url", cfg.SML.BaseURL))
	} else {
		logger.Info("sml sync worker disabled (SML_API_KEY not set)")
	}

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(middleware.RequestID())
	r.Use(middleware.Logger(logger))

	// Health — no auth.
	h := handlers.NewHealthHandler(pool, store)
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
	// File-view routes additionally accept ?token= (GET only) so the browser can
	// load PDFs in <iframe>/<a> where an Authorization header cannot be set.
	requireAuthFile := middleware.RequireAuthAllowQueryToken(cfg.Auth.JWTSecret)
	docH := handlers.NewDocumentHandler(pool, store, wfEngine, logger)
	attachH := handlers.NewAttachmentHandler(pool, store, logger)
	auditH := handlers.NewAuditHandler(pool, wfEngine)
	extSignerH := handlers.NewExternalSignerHandler(pool, logger)
	extSignH := handlers.NewExternalSignHandler(pool, store, wfEngine, logger)

	// Public external-sign endpoints — NO JWT. Token carried in X-Signer-Token header.
	// These are the ONLY unauthenticated routes; every other route stays behind requireAuth.
	extG := v1.Group("/external")
	{
		extG.GET("/document", extSignH.DocumentView)
		extG.GET("/document/file/original", extSignH.DownloadOriginalPublic)
		extG.POST("/sign", extSignH.Sign)
	}

	docsG := v1.Group("/documents", requireAuth)
	{
		docsG.GET("",
			middleware.RequireRole("document_admin", "system_admin", "auditor"),
			docH.List)
		docsG.POST("/import", docH.Import)
		docsG.GET("/:id", docH.Get)
		docsG.POST("/:id/attachments", attachH.Upload)
		docsG.GET("/:id/attachments", attachH.List)
		docsG.GET("/:id/audit-logs", auditH.AuditLogs)
		docsG.GET("/:id/workflow-status", auditH.WorkflowStatus)
		docsG.POST("/:id/external-signers",
			middleware.RequireRole("document_admin", "system_admin"), extSignerH.Invite)
		docsG.GET("/:id/external-signers",
			middleware.RequireRole("document_admin", "system_admin", "auditor"), extSignerH.List)
		docsG.POST("/:id/external-signers/:signerId/cancel",
			middleware.RequireRole("document_admin", "system_admin"), extSignerH.Cancel)
		docsG.POST("/:id/external-signers/:signerId/resend",
			middleware.RequireRole("document_admin", "system_admin"), extSignerH.Resend)
		docsG.POST("/:id/finalize",
			middleware.RequireRole("document_admin", "system_admin"), docH.Finalize)
	}

	// Workflow templates (auth required)
	wfTmplH := handlers.NewWorkflowTemplateHandler(pool, logger)
	wfTmplG := v1.Group("/workflow-templates", requireAuth,
		middleware.RequireRole("workflow_admin", "system_admin"))
	{
		wfTmplG.GET("", wfTmplH.ListTemplates)
		wfTmplG.GET("/:id", wfTmplH.GetTemplate)
		wfTmplG.POST("", wfTmplH.Create)
		wfTmplG.PUT("/:id", wfTmplH.Update)
		wfTmplG.PUT("/:id/steps", wfTmplH.UpdateSteps)
		wfTmplG.POST("/:id/clone", wfTmplH.Clone)
		wfTmplG.POST("/:id/publish", wfTmplH.Publish)
		wfTmplG.POST("/:id/deactivate", wfTmplH.Deactivate)
	}

	// Users — list active users for the workflow editor's assignee picker.
	userH := handlers.NewUserHandler(pool, logger)
	v1.GET("/users", requireAuth,
		middleware.RequireRole("workflow_admin", "system_admin"), userH.List)

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

	// PDF file routes — registered directly on v1 (not the docsG group) so they
	// use requireAuthFile (header OR ?token= for GET) instead of the group's
	// header-only requireAuth. Both handlers still enforce per-document access via
	// canAccessDocument; DownloadFinal additionally requires status='completed'.
	v1.GET("/documents/:id/file/original", requireAuthFile, docH.DownloadOriginal)
	v1.GET("/documents/:id/file/final", requireAuthFile, docH.DownloadFinal)

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
	workerCancel() // stop sml worker before draining HTTP
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("shutdown error", zap.Error(err))
	}
}
