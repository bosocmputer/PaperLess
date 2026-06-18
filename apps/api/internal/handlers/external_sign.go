package handlers

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"paperless-api/internal/httpx"
	"paperless-api/internal/pdf"
	"paperless-api/internal/storage"
	"paperless-api/internal/workflow"
)

const (
	rateLimitWindow      = time.Minute
	rateLimitMaxAttempts = 20 // per IP per minute on public routes

	// rateLimiterJanitorInterval controls how often the background janitor sweeps
	// stale buckets. Two full windows ensures a bucket is never evicted mid-window.
	// IMPORTANT — per-process in-memory caveat: this limiter is scoped to a single
	// API process. If you run multiple API instances behind a load balancer the
	// effective limit is N × rateLimitMaxAttempts (not shared). For the
	// single-instance pilot this is acceptable. Upgrade path: replace the buckets
	// map with a Redis-backed shared counter (e.g. redis INCR + EXPIRE per IP key).
	rateLimiterJanitorInterval = 2 * time.Minute
)

// ipBucket is a simple per-IP attempt counter with a rolling window.
type ipBucket struct {
	count    int
	windowAt time.Time
}

// ExternalSignHandler serves the unauthenticated public external-sign endpoints.
// These are the ONLY routes in PaperLess that do not require a JWT.
// Every input is treated as hostile. The token is a bearer credential sent as
// the X-Signer-Token request header (never in URL path or query string).
type ExternalSignHandler struct {
	pool  *pgxpool.Pool
	store *storage.Client
	eng   *workflow.Engine
	log   *zap.Logger

	mu      sync.Mutex
	buckets map[string]*ipBucket
}

func NewExternalSignHandler(pool *pgxpool.Pool, store *storage.Client, eng *workflow.Engine, log *zap.Logger) *ExternalSignHandler {
	h := &ExternalSignHandler{
		pool:    pool,
		store:   store,
		eng:     eng,
		log:     log,
		buckets: make(map[string]*ipBucket),
	}
	h.startJanitor()
	return h
}

// startJanitor launches a background goroutine that evicts buckets whose window
// has fully elapsed. Without eviction the buckets map grows unbounded — every
// IP that ever hit a public route stays in memory forever (slow leak + trivial
// memory-exhaustion vector). The janitor runs every rateLimiterJanitorInterval
// and holds the lock only for the sweep duration (typically < 1ms at pilot scale).
func (h *ExternalSignHandler) startJanitor() {
	go func() {
		ticker := time.NewTicker(rateLimiterJanitorInterval)
		defer ticker.Stop()
		for range ticker.C {
			h.evictStaleBuckets()
		}
	}()
}

// evictStaleBuckets removes entries whose window elapsed more than one full
// window ago (i.e. they are safe to discard — any new request from that IP
// will start a fresh bucket).
func (h *ExternalSignHandler) evictStaleBuckets() {
	h.mu.Lock()
	defer h.mu.Unlock()
	cutoff := time.Now().Add(-rateLimitWindow)
	for ip, b := range h.buckets {
		if b.windowAt.Before(cutoff) {
			delete(h.buckets, ip)
		}
	}
}

// checkRateLimit returns true when the caller exceeds the per-IP window limit.
func (h *ExternalSignHandler) checkRateLimit(ip string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	b, ok := h.buckets[ip]
	if !ok || time.Since(b.windowAt) > rateLimitWindow {
		h.buckets[ip] = &ipBucket{count: 1, windowAt: time.Now()}
		return false
	}
	b.count++
	return b.count > rateLimitMaxAttempts
}

// tokenHash reads X-Signer-Token header, validates its format, and returns the
// SHA-256 hex hash. Returns ("", errorCode) on any problem.
// The raw token is never logged.
func tokenHash(c *gin.Context) (hash string, errCode string) {
	raw := strings.TrimSpace(c.GetHeader("X-Signer-Token"))
	if raw == "" {
		return "", "external_link_invalid"
	}
	// Guard oversized / garbage input. Hex-encoded 32-byte token = 64 chars.
	if len(raw) > 256 {
		return "", "external_link_invalid"
	}
	b, err := hex.DecodeString(raw)
	if err != nil || len(b) < 16 {
		return "", "external_link_invalid"
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), ""
}

// checkTokenState checks expiry and status; returns an error code string or "".
func checkTokenState(status string, expiresAt time.Time) string {
	if time.Now().After(expiresAt) {
		return "external_link_expired"
	}
	switch status {
	case "signed":
		return "external_link_used"
	case "pending":
		return ""
	default:
		return "external_link_expired"
	}
}

// DocumentView godoc: GET /external/document
// Header: X-Signer-Token
// Returns document metadata if token is valid, unused, and unexpired.
// On any problem returns a stable machine code without revealing whether the doc exists.
func (h *ExternalSignHandler) DocumentView(c *gin.Context) {
	if h.checkRateLimit(c.ClientIP()) {
		httpx.Error(c, http.StatusTooManyRequests, "rate_limited", "too many requests")
		return
	}

	hash, errCode := tokenHash(c)
	if errCode != "" {
		httpx.Error(c, http.StatusUnauthorized, errCode, "invalid or missing token")
		return
	}
	ctx := c.Request.Context()

	var docID int64
	var docNo, docFormat, signerName, expiresAtStr string
	var extStatus string
	var expiresAt time.Time
	var taskID int64

	err := h.pool.QueryRow(ctx, `
		SELECT d.id, d.doc_no, d.doc_format_code,
		       es.name, es.status, es.token_expires_at, es.token_expires_at::text,
		       st.id
		  FROM external_signers es
		  JOIN signature_tasks st ON st.external_signer_id = es.id
		  JOIN documents d ON d.id = es.document_id
		 WHERE es.token_hash=$1
	`, hash).Scan(&docID, &docNo, &docFormat, &signerName, &extStatus, &expiresAt, &expiresAtStr, &taskID)

	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(c, http.StatusUnauthorized, "external_link_invalid", "invalid or missing token")
		return
	}
	if err != nil {
		h.log.Error("external view: lookup", zap.Error(err))
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "fetch failed")
		return
	}

	if code := checkTokenState(extStatus, expiresAt); code != "" {
		httpx.Error(c, http.StatusGone, code, "this signing link is no longer valid")
		return
	}

	httpx.OK(c, http.StatusOK, gin.H{
		"doc_id":      docID,
		"doc_no":      docNo,
		"doc_format_code": docFormat,
		"signer_name": signerName,
		"expires_at":  expiresAtStr,
		"task_id":     taskID,
	})
}

// DownloadOriginalPublic godoc: GET /external/document/file/original
// Header: X-Signer-Token
// Streams the original PDF after token check. Does NOT reuse the auth-only route.
// Does NOT expose a public doc-id URL.
func (h *ExternalSignHandler) DownloadOriginalPublic(c *gin.Context) {
	if h.checkRateLimit(c.ClientIP()) {
		httpx.Error(c, http.StatusTooManyRequests, "rate_limited", "too many requests")
		return
	}

	hash, errCode := tokenHash(c)
	if errCode != "" {
		httpx.Error(c, http.StatusUnauthorized, errCode, "invalid or missing token")
		return
	}
	ctx := c.Request.Context()

	var docID int64
	var objectKey, extStatus string
	var expiresAt time.Time

	err := h.pool.QueryRow(ctx, `
		SELECT es.document_id, es.status, es.token_expires_at, df.object_key
		  FROM external_signers es
		  JOIN document_files df ON df.document_id = es.document_id AND df.file_type='original_pdf'
		 WHERE es.token_hash=$1
		 LIMIT 1
	`, hash).Scan(&docID, &extStatus, &expiresAt, &objectKey)

	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(c, http.StatusUnauthorized, "external_link_invalid", "invalid or missing token")
		return
	}
	if err != nil {
		h.log.Error("external download: lookup", zap.Error(err))
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "download failed")
		return
	}

	// Allow `signed` signers to still view the original (audit reference).
	// Block expired/cancelled.
	if extStatus == "expired" || extStatus == "cancelled" {
		httpx.Error(c, http.StatusGone, "external_link_expired", "this signing link is no longer valid")
		return
	}
	if time.Now().After(expiresAt) {
		httpx.Error(c, http.StatusGone, "external_link_expired", "this signing link has expired")
		return
	}

	rc, size, err := h.store.Get(ctx, objectKey)
	if err != nil {
		h.log.Error("external download: get from storage", zap.Error(err), zap.Int64("doc_id", docID))
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "download failed")
		return
	}
	defer rc.Close()

	c.DataFromReader(http.StatusOK, size, "application/pdf", rc, nil)
}

// Sign godoc: POST /external/sign
// Header: X-Signer-Token
// Body: {"signature_image_hash": "...", "consent_text": "...", "request_id": "..."}
func (h *ExternalSignHandler) Sign(c *gin.Context) {
	if h.checkRateLimit(c.ClientIP()) {
		httpx.Error(c, http.StatusTooManyRequests, "rate_limited", "too many requests")
		return
	}

	hash, errCode := tokenHash(c)
	if errCode != "" {
		httpx.Error(c, http.StatusUnauthorized, errCode, "invalid or missing token")
		return
	}

	var body struct {
		SignatureImageHash string `json:"signature_image_hash"`
		ConsentText        string `json:"consent_text"`
		RequestID          string `json:"request_id"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_request", "invalid JSON body")
		return
	}
	if body.SignatureImageHash == "" {
		httpx.Error(c, http.StatusBadRequest, "signature_required", "signature_image_hash is required")
		return
	}
	// Cap hash length: SHA-512 hex = 128 chars; allow up to 256 for flexibility.
	// Reject oversized payloads before any DB work.
	if len(body.SignatureImageHash) > 256 {
		httpx.Error(c, http.StatusBadRequest, "invalid_request", "signature_image_hash exceeds maximum length")
		return
	}
	if body.RequestID == "" {
		httpx.Error(c, http.StatusBadRequest, "invalid_request", "request_id is required")
		return
	}
	if len(body.RequestID) > 128 {
		httpx.Error(c, http.StatusBadRequest, "invalid_request", "request_id exceeds maximum length")
		return
	}

	ctx := c.Request.Context()

	// Resolve task + state from token hash before calling engine.
	var taskID int64
	var extStatus, signerName string
	var expiresAt time.Time

	err := h.pool.QueryRow(ctx, `
		SELECT st.id, es.status, es.token_expires_at, es.name
		  FROM external_signers es
		  JOIN signature_tasks st ON st.external_signer_id = es.id
		 WHERE es.token_hash=$1
	`, hash).Scan(&taskID, &extStatus, &expiresAt, &signerName)

	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(c, http.StatusUnauthorized, "external_link_invalid", "invalid or missing token")
		return
	}
	if err != nil {
		h.log.Error("external sign: lookup", zap.Error(err))
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "sign failed")
		return
	}
	if code := checkTokenState(extStatus, expiresAt); code != "" {
		httpx.Error(c, http.StatusGone, code, "this signing link is no longer valid")
		return
	}

	if err := h.eng.ExternalSign(ctx, taskID, hash, workflow.SignInput{
		TaskID:             taskID,
		ExternalSignerName: signerName,
		SignatureImageHash: body.SignatureImageHash,
		ConsentText:        body.ConsentText,
		IPAddress:          c.ClientIP(),
		UserAgent:          c.GetHeader("User-Agent"),
		RequestID:          body.RequestID,
	}); err != nil {
		var expErr workflow.ErrExternalTokenExpired
		var alreadyActioned workflow.ErrStepAlreadyActioned
		switch {
		case errors.As(err, &expErr):
			httpx.Error(c, http.StatusGone, "external_link_expired", "token has expired")
		case errors.As(err, &alreadyActioned):
			httpx.Error(c, http.StatusGone, "external_link_used", "document already signed")
		default:
			h.log.Error("external sign: engine", zap.Error(err), zap.Int64("task_id", taskID))
			httpx.Error(c, http.StatusInternalServerError, "internal_error", "sign failed")
		}
		return
	}

	// Finalize PDF if document completed.
	var docID int64
	var docStatus string
	_ = h.pool.QueryRow(ctx, `
		SELECT st.document_id, d.status
		  FROM signature_tasks st
		  JOIN documents d ON d.id = st.document_id
		 WHERE st.id=$1
	`, taskID).Scan(&docID, &docStatus)

	if docStatus == "completed" {
		if _, ferr := pdf.FinalizeDocument(ctx, h.pool, h.store, docID); ferr != nil {
			h.log.Error("external sign: finalize PDF", zap.Error(ferr), zap.Int64("doc_id", docID))
		}
	}

	httpx.OK(c, http.StatusOK, gin.H{"signed": true})
}
