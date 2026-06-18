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
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"paperless-api/internal/httpx"
	"paperless-api/internal/middleware"
	"paperless-api/internal/pdf"
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
	if len(docFormatCode) > 50 {
		httpx.Error(c, http.StatusBadRequest, "invalid_request", "doc_format_code must be 50 characters or fewer")
		return
	}
	if len(docNo) > 100 {
		httpx.Error(c, http.StatusBadRequest, "invalid_request", "doc_no must be 100 characters or fewer")
		return
	}

	revision := 0
	if r := c.PostForm("revision"); r != "" {
		if v, err := strconv.Atoi(r); err == nil {
			revision = v
		}
	}

	// Validate optional fields before any DB work so parse failures surface as
	// clean 4xx rather than a Postgres cast error (which would be a 500).
	if ds := strings.TrimSpace(c.PostForm("doc_date")); ds != "" {
		if _, err := parseDate(ds); err != nil {
			httpx.Error(c, http.StatusBadRequest, "invalid_request", "doc_date must be in YYYY-MM-DD format")
			return
		}
	}
	if as := strings.TrimSpace(c.PostForm("amount")); as != "" {
		if err := validateDecimal(as); err != nil {
			httpx.Error(c, http.StatusBadRequest, "invalid_request", "amount must be a valid decimal number")
			return
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
		VALUES ('user', $1, 'document_imported', 'document', $2)
	`, actorID, strconv.FormatInt(docID, 10))

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

// validDocumentStatuses is the set of allowed values for the documents.status CHECK.
// Source: 0001_init.up.sql — CHECK (status IN ('imported','pending','rejected','completed','cancelled'))
var validDocumentStatuses = map[string]struct{}{
	"imported": {}, "pending": {}, "rejected": {}, "completed": {}, "cancelled": {},
}

// validSyncStatuses is the set of allowed values for documents.sync_status CHECK.
// Source: 0001_init.up.sql — CHECK (sync_status IN ('not_required','sync_pending','synced','sync_failed','sync_unknown'))
var validSyncStatuses = map[string]struct{}{
	"not_required": {}, "sync_pending": {}, "synced": {}, "sync_failed": {}, "sync_unknown": {},
}

// List godoc: GET /documents
// Paginated list of all documents. Supports filtering by status, doc_format_code,
// sync_status and substring search on doc_no (q). Admin/auditor only.
func (h *DocumentHandler) List(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "20"))
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 100 {
		size = 20
	}
	offset := (page - 1) * size

	// Validate optional enum filters before hitting the DB.
	statusFilter := c.Query("status")
	if statusFilter != "" {
		if _, ok := validDocumentStatuses[statusFilter]; !ok {
			httpx.Error(c, http.StatusBadRequest, "invalid_request",
				fmt.Sprintf("status %q is not a valid value; must be one of: imported,pending,rejected,completed,cancelled", statusFilter))
			return
		}
	}
	syncStatusFilter := c.Query("sync_status")
	if syncStatusFilter != "" {
		if _, ok := validSyncStatuses[syncStatusFilter]; !ok {
			httpx.Error(c, http.StatusBadRequest, "invalid_request",
				fmt.Sprintf("sync_status %q is not a valid value; must be one of: not_required,sync_pending,synced,sync_failed,sync_unknown", syncStatusFilter))
			return
		}
	}
	docFormatFilter := c.Query("doc_format_code")
	qFilter := c.Query("q")

	ctx := c.Request.Context()

	// Build WHERE clause dynamically to hit ix_documents_search.
	// Positional args: we accumulate args alongside the WHERE fragments.
	args := []any{}
	where := []string{}
	argN := 1

	if statusFilter != "" {
		where = append(where, fmt.Sprintf("status=$%d", argN))
		args = append(args, statusFilter)
		argN++
	}
	if docFormatFilter != "" {
		where = append(where, fmt.Sprintf("doc_format_code=$%d", argN))
		args = append(args, docFormatFilter)
		argN++
	}
	if syncStatusFilter != "" {
		where = append(where, fmt.Sprintf("sync_status=$%d", argN))
		args = append(args, syncStatusFilter)
		argN++
	}
	if qFilter != "" {
		// q is a literal substring match on doc_no. Escape LIKE metacharacters
		// (\ % _) so a doc_no like "PO_2567_001" is matched literally rather than
		// treating "_" / "%" as wildcards. ESCAPE '\' makes the backslash the
		// escape char in the pattern.
		where = append(where, fmt.Sprintf(`doc_no ILIKE $%d ESCAPE '\'`, argN))
		args = append(args, "%"+escapeLike(qFilter)+"%")
		argN++
	}

	whereClause := ""
	if len(where) > 0 {
		whereClause = "WHERE " + strings.Join(where, " AND ")
	}

	var total int
	countSQL := fmt.Sprintf("SELECT COUNT(*) FROM documents %s", whereClause)
	if err := h.pool.QueryRow(ctx, countSQL, args...).Scan(&total); err != nil {
		h.log.Error("list documents: count", zap.Error(err))
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "list failed")
		return
	}

	listArgs := append(args, size, offset)
	listSQL := fmt.Sprintf(`
		SELECT id, doc_format_code, doc_no, revision, status, sync_status,
		       amount::text, doc_date::text, workflow_version, created_at::text
		  FROM documents
		  %s
		 ORDER BY created_at DESC, id DESC
		 LIMIT $%d OFFSET $%d
	`, whereClause, argN, argN+1)

	rows, err := h.pool.Query(ctx, listSQL, listArgs...)
	if err != nil {
		h.log.Error("list documents: query", zap.Error(err))
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "list failed")
		return
	}
	defer rows.Close()

	type docRow struct {
		ID              string  `json:"id"`
		DocFormatCode   string  `json:"doc_format_code"`
		DocNo           string  `json:"doc_no"`
		Revision        int     `json:"revision"`
		Status          string  `json:"status"`
		SyncStatus      *string `json:"sync_status"`
		Amount          *string `json:"amount"`
		DocDate         *string `json:"doc_date"`
		WorkflowVersion int     `json:"workflow_version"`
		CreatedAt       string  `json:"created_at"`
	}
	var docs []docRow
	for rows.Next() {
		var d docRow
		var rawID int64
		if err := rows.Scan(
			&rawID, &d.DocFormatCode, &d.DocNo, &d.Revision, &d.Status, &d.SyncStatus,
			&d.Amount, &d.DocDate, &d.WorkflowVersion, &d.CreatedAt,
		); err != nil {
			h.log.Error("list documents: scan", zap.Error(err))
			httpx.Error(c, http.StatusInternalServerError, "internal_error", "list failed")
			return
		}
		d.ID = strconv.FormatInt(rawID, 10)
		docs = append(docs, d)
	}
	if docs == nil {
		docs = []docRow{}
	}

	httpx.List(c, http.StatusOK, docs, httpx.Meta{Total: total, Page: page, Size: size})
}

// Get godoc: GET /documents/:id
// Returns the document detail sufficient for both the signer task view and the
// admin detail view. idempotency_key is intentionally excluded (internal dedup
// key, not meaningful to callers). amount and doc_date are NULLable — scanned as
// *string so a NULL doesn't blow up the scan.
func (h *DocumentHandler) Get(c *gin.Context) {
	docID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_id", "document id must be an integer")
		return
	}
	ctx := c.Request.Context()

	var doc struct {
		ID              int64   `json:"id"`
		DocFormatCode   string  `json:"doc_format_code"`
		DocNo           string  `json:"doc_no"`
		Revision        int     `json:"revision"`
		Status          string  `json:"status"`
		SyncStatus      *string `json:"sync_status"`
		Amount          *string `json:"amount"`
		DocDate         *string `json:"doc_date"`
		WorkflowVersion int     `json:"workflow_version"`
		CreatedAt       string  `json:"created_at"`
	}
	err = h.pool.QueryRow(ctx,
		`SELECT id, doc_format_code, doc_no, revision, status, sync_status,
		        amount::text, doc_date::text, workflow_version, created_at::text
		   FROM documents WHERE id=$1`, docID,
	).Scan(
		&doc.ID, &doc.DocFormatCode, &doc.DocNo, &doc.Revision, &doc.Status, &doc.SyncStatus,
		&doc.Amount, &doc.DocDate, &doc.WorkflowVersion, &doc.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(c, http.StatusNotFound, "not_found", "document not found")
		return
	}
	if err != nil {
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "fetch failed")
		return
	}

	// Access scoping (mirrors GetTask): admin/auditor/workflow roles may read any
	// document; a plain signer may read a document only if they have a signature
	// task assigned to them on it. This prevents a signer from harvesting arbitrary
	// documents' details (incl. amount) by iterating ids. The legitimate signer UI
	// only ever opens documents reached from the user's own inbox, so this does not
	// break that flow.
	claims := middleware.ClaimsFrom(c)
	if !hasRole(claims, "system_admin", "document_admin", "auditor", "workflow_admin") {
		if claims == nil {
			httpx.Error(c, http.StatusForbidden, "forbidden", "not authorized to view this document")
			return
		}
		var hasTask bool
		if err := h.pool.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM signature_tasks WHERE document_id=$1 AND assigned_user_id=$2)`,
			docID, claims.UserID,
		).Scan(&hasTask); err != nil {
			httpx.Error(c, http.StatusInternalServerError, "internal_error", "fetch failed")
			return
		}
		if !hasTask {
			httpx.Error(c, http.StatusForbidden, "forbidden", "not authorized to view this document")
			return
		}
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
		// Doc is completed but the final PDF was never written (storage was down
		// at completion time). Return a stable code so the UI can offer a retry
		// instead of showing a generic 404.
		httpx.Error(c, http.StatusConflict, "pdf_generation_pending", "final PDF not yet generated — retry via POST /finalize")
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

// Finalize godoc: POST /documents/:id/finalize
// Idempotent: re-runs FinalizeDocument. No-ops if the final PDF already exists.
// Intended as the manual recovery path when storage was down at completion time.
// Requires: document_admin or system_admin role.
func (h *DocumentHandler) Finalize(c *gin.Context) {
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
		httpx.Error(c, http.StatusConflict, "document_not_completed", "finalize is only available once the document is completed")
		return
	}

	objectKey, err := pdf.FinalizeDocument(ctx, h.pool, h.store, docID)
	if err != nil {
		h.log.Error("finalize document", zap.Error(err), zap.Int64("doc_id", docID))
		httpx.Error(c, http.StatusInternalServerError, "pdf_generation_failed", "PDF generation failed")
		return
	}

	httpx.OK(c, http.StatusOK, gin.H{
		"doc_id":     docID,
		"object_key": objectKey,
		"status":     "ready",
	})
}

// escapeLike escapes the LIKE/ILIKE metacharacters (backslash, percent,
// underscore) so a user-supplied string is matched as a literal substring.
// The backslash itself must be escaped first. Used with ESCAPE '\' in the query.
func escapeLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, "%", `\%`)
	s = strings.ReplaceAll(s, "_", `\_`)
	return s
}

func nullablePostForm(c *gin.Context, key string) *string {
	v := strings.TrimSpace(c.PostForm(key))
	if v == "" {
		return nil
	}
	return &v
}

// parseDate validates YYYY-MM-DD format; returns error on invalid input.
func parseDate(s string) (string, error) {
	if len(s) != 10 || s[4] != '-' || s[7] != '-' {
		return "", fmt.Errorf("invalid date format")
	}
	// time.Parse validates the actual calendar values.
	if _, err := time.Parse("2006-01-02", s); err != nil {
		return "", err
	}
	return s, nil
}

// validateDecimal rejects strings that are not a valid decimal number
// (optional leading minus, digits, optional single decimal point).
func validateDecimal(s string) error {
	if s == "" {
		return nil
	}
	start := 0
	if s[0] == '-' {
		start = 1
	}
	if start == len(s) {
		return fmt.Errorf("invalid decimal")
	}
	dotSeen := false
	for _, ch := range s[start:] {
		if ch == '.' {
			if dotSeen {
				return fmt.Errorf("invalid decimal")
			}
			dotSeen = true
			continue
		}
		if ch < '0' || ch > '9' {
			return fmt.Errorf("invalid decimal")
		}
	}
	return nil
}
