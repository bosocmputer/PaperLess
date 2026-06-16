package handlers

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"paperless-api/internal/httpx"
	"paperless-api/internal/middleware"
	"paperless-api/internal/storage"
	"paperless-api/internal/workflow"
)

const (
	maxUploadBytes  = 50 << 20 // 50 MB
	allowedMIME     = "application/pdf"
)

type DocumentHandler struct {
	pool    *pgxpool.Pool
	store   *storage.Client
	engine  *workflow.Engine
	log     *zap.Logger
}

func NewDocumentHandler(pool *pgxpool.Pool, store *storage.Client, engine *workflow.Engine, log *zap.Logger) *DocumentHandler {
	return &DocumentHandler{pool: pool, store: store, engine: engine, log: log}
}

// Import godoc: POST /documents/import
// Multipart: file (PDF), doc_format_code, doc_no, revision (optional), doc_date (optional), amount (optional), source_doc_no (optional)
func (h *DocumentHandler) Import(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)

	// Parse multipart (max 50 MB).
	if err := c.Request.ParseMultipartForm(maxUploadBytes); err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_form", "could not parse multipart form")
		return
	}

	docFormatCode := strings.TrimSpace(c.PostForm("doc_format_code"))
	docNo := strings.TrimSpace(c.PostForm("doc_no"))
	if docFormatCode == "" || docNo == "" {
		httpx.Error(c, http.StatusBadRequest, "invalid_request", "doc_format_code and doc_no are required")
		return
	}

	revision := 0
	if r := c.PostForm("revision"); r != "" {
		if v, err := strconv.Atoi(r); err == nil {
			revision = v
		}
	}

	// Read file.
	fh, err := c.FormFile("file")
	if err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_request", "file is required")
		return
	}
	if fh.Size > maxUploadBytes {
		httpx.Error(c, http.StatusRequestEntityTooLarge, "file_too_large", "PDF must be under 50 MB")
		return
	}

	f, err := fh.Open()
	if err != nil {
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "could not open upload")
		return
	}
	defer f.Close()

	pdfBytes, err := io.ReadAll(io.LimitReader(f, maxUploadBytes+1))
	if err != nil {
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "could not read upload")
		return
	}
	if http.DetectContentType(pdfBytes) != allowedMIME && !bytes.HasPrefix(pdfBytes, []byte("%PDF")) {
		httpx.Error(c, http.StatusBadRequest, "invalid_file_type", "only PDF files are accepted")
		return
	}

	// Compute idempotency_key and source_hash.
	idempotencyKey := fmt.Sprintf("%s:%s:%d", docFormatCode, docNo, revision)

	metaJSON, _ := json.Marshal(map[string]any{
		"doc_format_code": docFormatCode,
		"doc_no":          docNo,
		"revision":        revision,
		"doc_date":        c.PostForm("doc_date"),
		"amount":          c.PostForm("amount"),
	})
	rawHash := sha256.Sum256(append(pdfBytes, metaJSON...))
	sourceHash := fmt.Sprintf("%x", rawHash)

	ctx := c.Request.Context()

	// Deduplicate inside a transaction.
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		h.log.Error("import: begin tx", zap.Error(err))
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "import failed")
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// Check for existing doc with the same idempotency_key.
	var existingID int64
	var existingHash string
	var existingStatus string
	err = tx.QueryRow(ctx,
		`SELECT id, source_hash, status FROM documents WHERE idempotency_key=$1`, idempotencyKey,
	).Scan(&existingID, &existingHash, &existingStatus)

	if err == nil {
		// Row exists.
		if existingHash == sourceHash {
			// Exact duplicate (retry) — return existing.
			tx.Rollback(ctx)
			h.log.Info("import: duplicate (same hash)", zap.String("idempotency_key", idempotencyKey))
			httpx.OK(c, http.StatusOK, gin.H{"id": existingID, "status": existingStatus, "duplicate": true})
			return
		}
		// Same key, different hash → revision conflict.
		tx.Rollback(ctx)
		httpx.Error(c, http.StatusConflict, "revision_conflict",
			fmt.Sprintf("document %s already exists with a different content hash", idempotencyKey))
		return
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		h.log.Error("import: lookup existing", zap.Error(err))
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "import failed")
		return
	}

	// Find the active workflow template for this doc_format_code.
	var templateID int64
	var templateVersion int
	err = tx.QueryRow(ctx, `
		SELECT id, version FROM workflow_templates
		 WHERE doc_format_code=$1 AND status='active'
	`, docFormatCode).Scan(&templateID, &templateVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(c, http.StatusUnprocessableEntity, "workflow_config_missing",
			fmt.Sprintf("no active workflow template for doc_format_code %q", docFormatCode))
		return
	}
	if err != nil {
		h.log.Error("import: find template", zap.Error(err))
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "import failed")
		return
	}

	// Insert document.
	docDate := nullablePostForm(c, "doc_date")
	amount := nullablePostForm(c, "amount")
	sourceDocNo := nullablePostForm(c, "source_doc_no")

	var docID int64
	err = tx.QueryRow(ctx, `
		INSERT INTO documents
		       (doc_format_code, doc_no, revision, doc_date, amount, source_doc_no,
		        workflow_template_id, workflow_version,
		        status, idempotency_key, source_hash)
		VALUES ($1, $2, $3, $4::date, $5::numeric, $6,
		        $7, $8,
		        'pending', $9, $10)
		RETURNING id
	`, docFormatCode, docNo, revision, docDate, amount, sourceDocNo,
		templateID, templateVersion, idempotencyKey, sourceHash,
	).Scan(&docID)
	if err != nil {
		h.log.Error("import: insert doc", zap.Error(err))
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "import failed")
		return
	}

	// Store PDF in MinIO.
	objectKey := fmt.Sprintf("documents/%d/original.pdf", docID)
	if err := h.store.Put(ctx, objectKey, "application/pdf", bytes.NewReader(pdfBytes), int64(len(pdfBytes))); err != nil {
		h.log.Error("import: upload PDF", zap.Error(err), zap.Int64("doc_id", docID))
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "PDF storage failed")
		return
	}

	// Record the file.
	var fileID int64
	var uploaderID *int64
	if claims != nil {
		uploaderID = &claims.UserID
	}
	err = tx.QueryRow(ctx, `
		INSERT INTO document_files (document_id, file_type, object_key, file_hash, mime_type, size_bytes, uploaded_by_user_id)
		VALUES ($1, 'original_pdf', $2, $3, 'application/pdf', $4, $5)
		RETURNING id
	`, docID, objectKey, sourceHash, int64(len(pdfBytes)), uploaderID).Scan(&fileID)
	if err != nil {
		h.log.Error("import: record file", zap.Error(err))
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "import failed")
		return
	}

	// Open first-sequence tasks.
	if err := workflow.OpenFirstSequence(ctx, tx, docID, templateID); err != nil {
		h.log.Error("import: open tasks", zap.Error(err))
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "import failed")
		return
	}

	// Write audit.
	var actorID string
	if claims != nil {
		actorID = strconv.FormatInt(claims.UserID, 10)
	} else {
		actorID = "system"
	}
	_, _ = tx.Exec(ctx, `
		INSERT INTO audit_logs (actor_type, actor_id, action, entity_type, entity_id)
		VALUES ('user', $1, 'document_imported', 'document', $2::text)
	`, actorID, docID)

	if err := tx.Commit(ctx); err != nil {
		h.log.Error("import: commit", zap.Error(err))
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "import failed")
		return
	}

	h.log.Info("document imported",
		zap.Int64("doc_id", docID),
		zap.String("idempotency_key", idempotencyKey),
	)
	httpx.OK(c, http.StatusCreated, gin.H{
		"id":              docID,
		"doc_format_code": docFormatCode,
		"doc_no":          docNo,
		"revision":        revision,
		"status":          "pending",
		"duplicate":       false,
	})
}

// Get godoc: GET /documents/:id
func (h *DocumentHandler) Get(c *gin.Context) {
	docID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_id", "document id must be an integer")
		return
	}
	ctx := c.Request.Context()

	var doc struct {
		ID             int64  `json:"id"`
		DocFormatCode  string `json:"doc_format_code"`
		DocNo          string `json:"doc_no"`
		Revision       int    `json:"revision"`
		Status         string `json:"status"`
		SyncStatus     *string `json:"sync_status"`
		IdempotencyKey string  `json:"idempotency_key"`
	}
	err = h.pool.QueryRow(ctx,
		`SELECT id, doc_format_code, doc_no, revision, status, sync_status, idempotency_key
		   FROM documents WHERE id=$1`, docID,
	).Scan(&doc.ID, &doc.DocFormatCode, &doc.DocNo, &doc.Revision, &doc.Status, &doc.SyncStatus, &doc.IdempotencyKey)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(c, http.StatusNotFound, "not_found", "document not found")
		return
	}
	if err != nil {
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "fetch failed")
		return
	}
	httpx.OK(c, http.StatusOK, doc)
}

// DownloadOriginal godoc: GET /documents/:id/file/original
func (h *DocumentHandler) DownloadOriginal(c *gin.Context) {
	docID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_id", "document id must be an integer")
		return
	}
	ctx := c.Request.Context()

	var objectKey string
	err = h.pool.QueryRow(ctx,
		`SELECT object_key FROM document_files WHERE document_id=$1 AND file_type='original_pdf' LIMIT 1`, docID,
	).Scan(&objectKey)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(c, http.StatusNotFound, "not_found", "original PDF not found")
		return
	}
	if err != nil {
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "fetch failed")
		return
	}

	rc, size, err := h.store.Get(ctx, objectKey)
	if err != nil {
		h.log.Error("download original", zap.Error(err), zap.Int64("doc_id", docID))
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "download failed")
		return
	}
	defer rc.Close()

	c.DataFromReader(http.StatusOK, size, "application/pdf", rc, map[string]string{
		"Content-Disposition": fmt.Sprintf(`inline; filename="doc_%d_original.pdf"`, docID),
	})
}

// DownloadFinal godoc: GET /documents/:id/file/final
// Returns the signature-evidence PDF. Document must be completed.
func (h *DocumentHandler) DownloadFinal(c *gin.Context) {
	docID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_id", "document id must be an integer")
		return
	}
	ctx := c.Request.Context()

	var docStatus string
	if err := h.pool.QueryRow(ctx, `SELECT status FROM documents WHERE id=$1`, docID).Scan(&docStatus); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			httpx.Error(c, http.StatusNotFound, "not_found", "document not found")
			return
		}
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "fetch failed")
		return
	}
	if docStatus != "completed" {
		httpx.Error(c, http.StatusConflict, "document_not_completed", "final PDF is only available once the document is completed")
		return
	}

	var objectKey string
	err = h.pool.QueryRow(ctx,
		`SELECT object_key FROM document_files WHERE document_id=$1 AND file_type='final_pdf' LIMIT 1`, docID,
	).Scan(&objectKey)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(c, http.StatusNotFound, "not_found", "final PDF not yet generated")
		return
	}
	if err != nil {
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "fetch failed")
		return
	}

	rc, size, err := h.store.Get(ctx, objectKey)
	if err != nil {
		h.log.Error("download final PDF", zap.Error(err), zap.Int64("doc_id", docID))
		httpx.Error(c, http.StatusInternalServerError, "pdf_preview_failed", "PDF download failed")
		return
	}
	defer rc.Close()

	c.DataFromReader(http.StatusOK, size, "application/pdf", rc, map[string]string{
		"Content-Disposition": fmt.Sprintf(`inline; filename="doc_%d_final.pdf"`, docID),
	})
}

func nullablePostForm(c *gin.Context, key string) *string {
	v := strings.TrimSpace(c.PostForm(key))
	if v == "" {
		return nil
	}
	return &v
}
