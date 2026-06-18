package handlers

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"paperless-api/internal/httpx"
	"paperless-api/internal/middleware"
)

const (
	defaultExpiryHours = 72
	maxExpiryHours     = 168 // 7 days cap
	tokenBytes         = 32  // 256-bit raw token (≥32 bytes per plan)
	maxNameLen         = 200
	maxEmailLen        = 254 // RFC 5321
	maxPhoneLen        = 30
)

var emailRE = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)

type ExternalSignerHandler struct {
	pool *pgxpool.Pool
	log  *zap.Logger
}

func NewExternalSignerHandler(pool *pgxpool.Pool, log *zap.Logger) *ExternalSignerHandler {
	return &ExternalSignerHandler{pool: pool, log: log}
}

// Invite godoc: POST /documents/:id/external-signers
// Body: {"name": "...", "email": "...", "phone": "...", "expires_in_hours": 72}
// Requires: document_admin role.
// Returns the raw token ONCE in the response — never stored, never logged.
func (h *ExternalSignerHandler) Invite(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)

	docID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_id", "document id must be an integer")
		return
	}

	var body struct {
		Name           string `json:"name"`
		Email          string `json:"email"`
		Phone          string `json:"phone"`
		ExpiresInHours int    `json:"expires_in_hours"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_request", "invalid JSON body")
		return
	}
	body.Name = strings.TrimSpace(body.Name)
	if body.Name == "" {
		httpx.Error(c, http.StatusBadRequest, "invalid_request", "name is required")
		return
	}
	if len(body.Name) > maxNameLen {
		httpx.Error(c, http.StatusBadRequest, "invalid_request", fmt.Sprintf("name must be %d characters or fewer", maxNameLen))
		return
	}
	if body.Email != "" {
		if len(body.Email) > maxEmailLen || !emailRE.MatchString(body.Email) {
			httpx.Error(c, http.StatusBadRequest, "invalid_request", "email format is invalid")
			return
		}
	}
	if len(body.Phone) > maxPhoneLen {
		httpx.Error(c, http.StatusBadRequest, "invalid_request", fmt.Sprintf("phone must be %d characters or fewer", maxPhoneLen))
		return
	}
	// expires_in_hours: negative values are treated as "use default"; values
	// above the cap are clamped. Zero means "not provided" (also default).
	expiry := defaultExpiryHours
	if body.ExpiresInHours > 0 {
		expiry = body.ExpiresInHours
	}
	if expiry > maxExpiryHours {
		expiry = maxExpiryHours
	}
	expiresAt := time.Now().UTC().Add(time.Duration(expiry) * time.Hour)

	ctx := c.Request.Context()

	// Generate cryptographically random token (32 bytes → 64-char hex).
	rawTokenBytes := make([]byte, tokenBytes)
	if _, err := rand.Read(rawTokenBytes); err != nil {
		h.log.Error("generate token", zap.Error(err))
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "token generation failed")
		return
	}
	rawToken := hex.EncodeToString(rawTokenBytes) // 64-char hex; returned once to caller
	hashBytes := sha256.Sum256(rawTokenBytes)
	tokenHash := hex.EncodeToString(hashBytes[:]) // stored in DB

	tx, err := h.pool.Begin(ctx)
	if err != nil {
		h.log.Error("invite: begin tx", zap.Error(err))
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "invite failed")
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// Verify the document exists and is pending.
	var docStatus string
	err = tx.QueryRow(ctx, `SELECT status FROM documents WHERE id=$1`, docID).Scan(&docStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(c, http.StatusNotFound, "not_found", "document not found")
		return
	}
	if err != nil {
		h.log.Error("invite: fetch doc", zap.Error(err))
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "invite failed")
		return
	}
	if docStatus != "pending" {
		httpx.Error(c, http.StatusConflict, "document_already_completed",
			fmt.Sprintf("document is %s; cannot invite external signer", docStatus))
		return
	}

	// Find the `waiting` external task for this document.
	// There must be exactly one un-linked waiting c3 task to activate.
	var taskID int64
	err = tx.QueryRow(ctx, `
		SELECT id FROM signature_tasks
		 WHERE document_id=$1 AND condition_type=3 AND status='waiting' AND external_signer_id IS NULL
		 LIMIT 1
		   FOR UPDATE
	`, docID).Scan(&taskID)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(c, http.StatusConflict, "external_signer_info_missing",
			"no uninvited external task found for this document (already invited or document has no external step)")
		return
	}
	if err != nil {
		h.log.Error("invite: find task", zap.Error(err))
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "invite failed")
		return
	}

	// Insert external_signers row (hash only — raw token never persisted).
	var extSignerID int64
	err = tx.QueryRow(ctx, `
		INSERT INTO external_signers (document_id, name, email, phone, token_hash, token_expires_at, status)
		VALUES ($1, $2, $3, $4, $5, $6, 'pending')
		RETURNING id
	`, docID, body.Name, nullableStr(body.Email), nullableStr(body.Phone), tokenHash, expiresAt,
	).Scan(&extSignerID)
	if err != nil {
		h.log.Error("invite: insert external_signer", zap.Error(err))
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "invite failed")
		return
	}

	// Link the waiting task: set external_signer_id, flip status to `open`.
	if _, err := tx.Exec(ctx, `
		UPDATE signature_tasks
		   SET external_signer_id=$1, status='open', opened_at=now(), version=version+1
		 WHERE id=$2
	`, extSignerID, taskID); err != nil {
		h.log.Error("invite: activate task", zap.Error(err))
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "invite failed")
		return
	}

	// Audit the invite (external_signer entity). Never log the raw token.
	_, _ = tx.Exec(ctx, `
		INSERT INTO audit_logs (actor_type, actor_id, action, entity_type, entity_id)
		VALUES ('user', $1, 'external_signer_invited', 'external_signer', $2)
	`, strconv.FormatInt(claims.UserID, 10), strconv.FormatInt(extSignerID, 10))

	if err := tx.Commit(ctx); err != nil {
		h.log.Error("invite: commit", zap.Error(err))
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "invite failed")
		return
	}

	h.log.Info("external signer invited",
		zap.Int64("doc_id", docID),
		zap.Int64("external_signer_id", extSignerID),
		zap.String("name", body.Name),
		// raw token intentionally NOT logged
	)

	// Return raw token once — caller must deliver it to the signer securely.
	httpx.OK(c, http.StatusCreated, gin.H{
		"external_signer_id": extSignerID,
		"task_id":            taskID,
		"name":               body.Name,
		"expires_at":         expiresAt.Format(time.RFC3339),
		"token":              rawToken, // returned ONCE; never stored; never logged
	})
}

// List godoc: GET /documents/:id/external-signers
// Returns signers and their status. Never exposes token or token_hash.
func (h *ExternalSignerHandler) List(c *gin.Context) {
	docID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_id", "document id must be an integer")
		return
	}
	ctx := c.Request.Context()

	// Verify document exists.
	var exists bool
	_ = h.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM documents WHERE id=$1)`, docID).Scan(&exists)
	if !exists {
		httpx.Error(c, http.StatusNotFound, "not_found", "document not found")
		return
	}

	type signerRow struct {
		ID           int64   `json:"id"`
		Name         string  `json:"name"`
		Email        *string `json:"email"`
		Phone        *string `json:"phone"`
		Status       string  `json:"status"`
		ExpiresAt    string  `json:"expires_at"`
		OTPVerified  bool    `json:"otp_verified"`
		CreatedAt    string  `json:"created_at"`
	}

	rows, err := h.pool.Query(ctx, `
		SELECT id, name, email, phone, status,
		       token_expires_at::text, (otp_verified_at IS NOT NULL), created_at::text
		  FROM external_signers
		 WHERE document_id=$1
		 ORDER BY created_at
		 LIMIT 100
	`, docID)
	if err != nil {
		h.log.Error("list external signers", zap.Error(err))
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "fetch failed")
		return
	}
	defer rows.Close()

	var signers []signerRow
	for rows.Next() {
		var s signerRow
		if err := rows.Scan(&s.ID, &s.Name, &s.Email, &s.Phone, &s.Status,
			&s.ExpiresAt, &s.OTPVerified, &s.CreatedAt); err != nil {
			h.log.Error("scan external signer row", zap.Error(err))
			httpx.Error(c, http.StatusInternalServerError, "internal_error", "fetch failed")
			return
		}
		signers = append(signers, s)
	}
	if err := rows.Err(); err != nil {
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "fetch failed")
		return
	}
	if signers == nil {
		signers = []signerRow{}
	}
	httpx.OK(c, http.StatusOK, signers)
}

// Cancel godoc: POST /documents/:id/external-signers/:signerId/cancel
// Requires: document_admin or system_admin.
// Sets the external_signer to 'cancelled' and returns the linked signature_task
// to 'waiting' (un-linking external_signer_id) so a fresh resend can activate it.
// Idempotent: cancelling an already-cancelled signer returns 200, never a 500.
func (h *ExternalSignerHandler) Cancel(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)

	docID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_id", "document id must be an integer")
		return
	}
	signerID, err := strconv.ParseInt(c.Param("signerId"), 10, 64)
	if err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_id", "signer id must be an integer")
		return
	}

	ctx := c.Request.Context()

	tx, err := h.pool.Begin(ctx)
	if err != nil {
		h.log.Error("cancel: begin tx", zap.Error(err))
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "cancel failed")
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// Verify doc exists and is not terminal.
	var docStatus string
	err = tx.QueryRow(ctx, `SELECT status FROM documents WHERE id=$1`, docID).Scan(&docStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(c, http.StatusNotFound, "not_found", "document not found")
		return
	}
	if err != nil {
		h.log.Error("cancel: fetch doc", zap.Error(err))
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "cancel failed")
		return
	}
	if docStatus == "completed" || docStatus == "rejected" || docStatus == "cancelled" {
		httpx.Error(c, http.StatusConflict, "document_terminal",
			fmt.Sprintf("document is %s; cannot cancel external signer", docStatus))
		return
	}

	// Load the signer row (lock it for update).
	var signerStatus string
	err = tx.QueryRow(ctx,
		`SELECT status FROM external_signers WHERE id=$1 AND document_id=$2 FOR UPDATE`,
		signerID, docID,
	).Scan(&signerStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(c, http.StatusNotFound, "not_found", "external signer not found")
		return
	}
	if err != nil {
		h.log.Error("cancel: fetch signer", zap.Error(err))
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "cancel failed")
		return
	}

	// Idempotent: already cancelled → return 200 immediately.
	if signerStatus == "cancelled" {
		if err := tx.Commit(ctx); err != nil {
			h.log.Error("cancel: commit (idempotent)", zap.Error(err))
		}
		httpx.OK(c, http.StatusOK, gin.H{"cancelled": true, "idempotent": true})
		return
	}

	// Signed signers cannot be cancelled.
	if signerStatus == "signed" {
		httpx.Error(c, http.StatusConflict, "signer_already_signed",
			"signer has already signed; cannot cancel")
		return
	}

	// Set signer to cancelled.
	if _, err := tx.Exec(ctx,
		`UPDATE external_signers SET status='cancelled' WHERE id=$1`, signerID,
	); err != nil {
		h.log.Error("cancel: update signer", zap.Error(err))
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "cancel failed")
		return
	}

	// Return the linked task to 'waiting' and un-link the signer.
	if _, err := tx.Exec(ctx, `
		UPDATE signature_tasks
		   SET status='waiting', external_signer_id=NULL, opened_at=NULL, version=version+1
		 WHERE document_id=$1 AND external_signer_id=$2 AND status NOT IN ('signed','skipped','cancelled','rejected')
	`, docID, signerID); err != nil {
		h.log.Error("cancel: reset task", zap.Error(err))
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "cancel failed")
		return
	}

	// Audit.
	_, _ = tx.Exec(ctx, `
		INSERT INTO audit_logs (actor_type, actor_id, action, entity_type, entity_id)
		VALUES ('user', $1, 'external_signer_cancelled', 'external_signer', $2)
	`, strconv.FormatInt(claims.UserID, 10), strconv.FormatInt(signerID, 10))

	if err := tx.Commit(ctx); err != nil {
		h.log.Error("cancel: commit", zap.Error(err))
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "cancel failed")
		return
	}

	h.log.Info("external signer cancelled",
		zap.Int64("doc_id", docID),
		zap.Int64("external_signer_id", signerID),
	)

	httpx.OK(c, http.StatusOK, gin.H{"cancelled": true})
}

// Resend godoc: POST /documents/:id/external-signers/:signerId/resend
// Requires: document_admin or system_admin.
// Issues a fresh 32-byte token, overwrites token_hash + token_expires_at in place.
// Only valid when signer is 'pending'. Returns the raw token ONCE — never re-fetchable,
// never logged. The old token becomes invalid immediately upon commit.
func (h *ExternalSignerHandler) Resend(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)

	docID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_id", "document id must be an integer")
		return
	}
	signerID, err := strconv.ParseInt(c.Param("signerId"), 10, 64)
	if err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_id", "signer id must be an integer")
		return
	}

	// Optional: allow caller to set a custom expiry; otherwise use the default.
	var body struct {
		ExpiresInHours int `json:"expires_in_hours"`
	}
	// Ignore parse errors — body is optional for resend.
	_ = c.ShouldBindJSON(&body)
	expiry := defaultExpiryHours
	if body.ExpiresInHours > 0 {
		expiry = body.ExpiresInHours
	}
	if expiry > maxExpiryHours {
		expiry = maxExpiryHours
	}
	expiresAt := time.Now().UTC().Add(time.Duration(expiry) * time.Hour)

	ctx := c.Request.Context()

	// Generate new token before the tx to avoid holding locks during crypto.
	rawTokenBytes := make([]byte, tokenBytes)
	if _, err := rand.Read(rawTokenBytes); err != nil {
		h.log.Error("resend: generate token", zap.Error(err))
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "token generation failed")
		return
	}
	rawToken := hex.EncodeToString(rawTokenBytes) // 64-char hex; returned once to caller
	hashBytes := sha256.Sum256(rawTokenBytes)
	newTokenHash := hex.EncodeToString(hashBytes[:]) // stored in DB

	tx, err := h.pool.Begin(ctx)
	if err != nil {
		h.log.Error("resend: begin tx", zap.Error(err))
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "resend failed")
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// Verify doc exists and is not terminal.
	var docStatus string
	err = tx.QueryRow(ctx, `SELECT status FROM documents WHERE id=$1`, docID).Scan(&docStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(c, http.StatusNotFound, "not_found", "document not found")
		return
	}
	if err != nil {
		h.log.Error("resend: fetch doc", zap.Error(err))
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "resend failed")
		return
	}
	if docStatus == "completed" || docStatus == "rejected" || docStatus == "cancelled" {
		httpx.Error(c, http.StatusConflict, "document_terminal",
			fmt.Sprintf("document is %s; cannot resend external signer link", docStatus))
		return
	}

	// Load and lock the signer.
	var signerStatus, signerName string
	err = tx.QueryRow(ctx,
		`SELECT status, name FROM external_signers WHERE id=$1 AND document_id=$2 FOR UPDATE`,
		signerID, docID,
	).Scan(&signerStatus, &signerName)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(c, http.StatusNotFound, "not_found", "external signer not found")
		return
	}
	if err != nil {
		h.log.Error("resend: fetch signer", zap.Error(err))
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "resend failed")
		return
	}

	// Only 'pending' signers can be resent.
	if signerStatus != "pending" {
		httpx.Error(c, http.StatusConflict, "signer_not_resendable",
			fmt.Sprintf("signer is %s; only pending signers can be resent", signerStatus))
		return
	}

	// Overwrite token_hash + expiry in place — old token becomes invalid immediately.
	if _, err := tx.Exec(ctx, `
		UPDATE external_signers
		   SET token_hash=$1, token_expires_at=$2
		 WHERE id=$3
	`, newTokenHash, expiresAt, signerID); err != nil {
		h.log.Error("resend: update token", zap.Error(err))
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "resend failed")
		return
	}

	// Audit. Never log the raw token.
	_, _ = tx.Exec(ctx, `
		INSERT INTO audit_logs (actor_type, actor_id, action, entity_type, entity_id)
		VALUES ('user', $1, 'external_signer_resent', 'external_signer', $2)
	`, strconv.FormatInt(claims.UserID, 10), strconv.FormatInt(signerID, 10))

	if err := tx.Commit(ctx); err != nil {
		h.log.Error("resend: commit", zap.Error(err))
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "resend failed")
		return
	}

	h.log.Info("external signer resent",
		zap.Int64("doc_id", docID),
		zap.Int64("external_signer_id", signerID),
		zap.String("name", signerName),
		// raw token intentionally NOT logged
	)

	// Return raw token once — caller must deliver it to the signer securely.
	httpx.OK(c, http.StatusOK, gin.H{
		"external_signer_id": signerID,
		"name":               signerName,
		"expires_at":         expiresAt.Format(time.RFC3339),
		"token":              rawToken, // returned ONCE; never stored; never logged
	})
}

func nullableStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
