package handlers

import (
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

	"paperless-api/internal/auth"
	"paperless-api/internal/httpx"
	"paperless-api/internal/middleware"
	"paperless-api/internal/storage"
)

const (
	maxAttachmentBytes = 20 << 20 // 20 MB
)

var allowedAttachmentMIMEs = map[string]bool{
	"application/pdf": true,
	"image/jpeg":      true,
	"image/png":       true,
	"image/gif":       true,
}

type AttachmentHandler struct {
	pool  *pgxpool.Pool
	store *storage.Client
	log   *zap.Logger
}

func NewAttachmentHandler(pool *pgxpool.Pool, store *storage.Client, log *zap.Logger) *AttachmentHandler {
	return &AttachmentHandler{pool: pool, store: store, log: log}
}

// Upload godoc: POST /documents/:id/attachments
func (h *AttachmentHandler) Upload(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	docID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_id", "document id must be an integer")
		return
	}
	ctx := c.Request.Context()

	// Confirm document exists and is accessible.
	var docStatus string
	err = h.pool.QueryRow(ctx, `SELECT status FROM documents WHERE id=$1`, docID).Scan(&docStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(c, http.StatusNotFound, "not_found", "document not found")
		return
	}
	if err != nil {
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "fetch failed")
		return
	}

	if err := c.Request.ParseMultipartForm(maxAttachmentBytes); err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_form", "could not parse form")
		return
	}

	fh, err := c.FormFile("file")
	if err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_request", "file is required")
		return
	}
	if fh.Size > maxAttachmentBytes {
		httpx.Error(c, http.StatusRequestEntityTooLarge, "file_too_large", "attachment must be under 20 MB")
		return
	}

	f, err := fh.Open()
	if err != nil {
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "could not open upload")
		return
	}
	defer f.Close()

	// Detect MIME from first 512 bytes, then re-read full file.
	buf := make([]byte, 512)
	n, _ := f.Read(buf)
	mime := http.DetectContentType(buf[:n])
	if _, ok := allowedAttachmentMIMEs[mime]; !ok {
		httpx.Error(c, http.StatusBadRequest, "invalid_file_type", "only PDF and image files are accepted")
		return
	}

	// Seek back to start and store.
	type seeker interface {
		io.Reader
		Seek(int64, int) (int64, error)
	}
	if s, ok := f.(seeker); ok {
		_, _ = s.Seek(0, 0)
	} else {
		// Can't seek — re-open (multipart files support this).
		f.Close()
		f2, _ := fh.Open()
		defer f2.Close()
		f = f2
	}

	objectKey := fmt.Sprintf("documents/%d/attachments/%s", docID, sanitizeFilename(fh.Filename))
	if err := h.store.Put(ctx, objectKey, mime, f, fh.Size); err != nil {
		h.log.Error("attachment upload", zap.Error(err), zap.Int64("doc_id", docID))
		httpx.Error(c, http.StatusInternalServerError, "attachment_upload_failed", "file storage failed")
		return
	}

	var fileID int64
	err = h.pool.QueryRow(ctx, `
		INSERT INTO document_files (document_id, file_type, object_key, mime_type, size_bytes, uploaded_by_user_id)
		VALUES ($1, 'attachment', $2, $3, $4, $5)
		RETURNING id
	`, docID, objectKey, mime, fh.Size, claims.UserID).Scan(&fileID)
	if err != nil {
		h.log.Error("attachment db insert", zap.Error(err))
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "attachment record failed")
		return
	}

	// Audit.
	_, _ = h.pool.Exec(ctx, `
		INSERT INTO audit_logs (actor_type, actor_id, action, entity_type, entity_id)
		VALUES ('user', $1, 'attachment_uploaded', 'file', $2)
	`, strconv.FormatInt(claims.UserID, 10), strconv.FormatInt(fileID, 10))

	httpx.OK(c, http.StatusCreated, gin.H{
		"id":         fileID,
		"object_key": objectKey,
		"mime_type":  mime,
		"size_bytes": fh.Size,
	})
}

// List godoc: GET /documents/:id/attachments
func (h *AttachmentHandler) List(c *gin.Context) {
	docID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_id", "document id must be an integer")
		return
	}
	ctx := c.Request.Context()

	rows, err := h.pool.Query(ctx, `
		SELECT id, object_key, mime_type, size_bytes, uploaded_by_user_id, created_at
		  FROM document_files
		 WHERE document_id=$1 AND file_type='attachment'
		 ORDER BY created_at
	`, docID)
	if err != nil {
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "list failed")
		return
	}
	defer rows.Close()

	type row struct {
		ID               int64   `json:"id"`
		ObjectKey        string  `json:"object_key"`
		MimeType         *string `json:"mime_type"`
		SizeBytes        *int64  `json:"size_bytes"`
		UploadedByUserID *int64  `json:"uploaded_by_user_id"`
		CreatedAt        string  `json:"created_at"`
	}
	var files []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.ID, &r.ObjectKey, &r.MimeType, &r.SizeBytes, &r.UploadedByUserID, &r.CreatedAt); err != nil {
			httpx.Error(c, http.StatusInternalServerError, "internal_error", "list scan failed")
			return
		}
		files = append(files, r)
	}
	if files == nil {
		files = []row{}
	}
	httpx.OK(c, http.StatusOK, files)
}

// Delete godoc: DELETE /attachments/:id
func (h *AttachmentHandler) Delete(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	fileID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_id", "file id must be an integer")
		return
	}
	ctx := c.Request.Context()

	var objectKey string
	var uploaderID *int64
	err = h.pool.QueryRow(ctx,
		`SELECT object_key, uploaded_by_user_id FROM document_files WHERE id=$1 AND file_type='attachment'`,
		fileID,
	).Scan(&objectKey, &uploaderID)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(c, http.StatusNotFound, "not_found", "attachment not found")
		return
	}
	if err != nil {
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "fetch failed")
		return
	}

	// Only uploader or admin may delete.
	if !hasAdminRole(claims) && (uploaderID == nil || *uploaderID != claims.UserID) {
		httpx.Error(c, http.StatusForbidden, "forbidden", "cannot delete this attachment")
		return
	}

	if err := h.store.Delete(ctx, objectKey); err != nil {
		h.log.Warn("attachment delete from minio", zap.Error(err), zap.Int64("file_id", fileID))
	}
	_, _ = h.pool.Exec(ctx, `DELETE FROM document_files WHERE id=$1`, fileID)

	_, _ = h.pool.Exec(ctx, `
		INSERT INTO audit_logs (actor_type, actor_id, action, entity_type, entity_id)
		VALUES ('user', $1, 'attachment_deleted', 'file', $2)
	`, strconv.FormatInt(claims.UserID, 10), strconv.FormatInt(fileID, 10))

	httpx.OK(c, http.StatusOK, gin.H{"message": "deleted"})
}

func hasAdminRole(claims *auth.Claims) bool {
	if claims == nil {
		return false
	}
	for _, r := range claims.Roles {
		if r == "system_admin" || r == "document_admin" {
			return true
		}
	}
	return false
}

func sanitizeFilename(name string) string {
	name = strings.ReplaceAll(name, "/", "_")
	name = strings.ReplaceAll(name, "..", "_")
	if name == "" {
		return "file"
	}
	return name
}
