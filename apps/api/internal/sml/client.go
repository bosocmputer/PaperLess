package sml

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	urlpkg "net/url"
	"time"

	"paperless-api/internal/config"
)

// ErrDocNotFound is returned when sml-api-bybos responds 404 — the document
// does not exist in SML. This is a permanent failure; do NOT retry.
var ErrDocNotFound = errors.New("sml: document not found")

// LockResult holds the decoded payload from a successful lock response.
type LockResult struct {
	DocNo        string `json:"doc_no"`
	Table        string `json:"table"`
	TransFlag    int    `json:"trans_flag"`
	IsLockRecord int    `json:"is_lock_record"`
	AlreadyLocked bool  `json:"already_locked"`
}

type lockResponse struct {
	Success bool       `json:"success"`
	Data    LockResult `json:"data"`
	Error   *struct {
		Code string `json:"code"`
	} `json:"error,omitempty"`
}

// Client calls the sml-api-bybos lock endpoint.
type Client struct {
	baseURL string
	apiKey  string
	tenant  string
	http    *http.Client
}

// NewClient constructs a Client from cfg.SML.*. Returns nil when APIKey is
// empty — the caller must gate worker startup on a non-nil client.
func NewClient(cfg *config.Config) *Client {
	if cfg.SML.APIKey == "" {
		return nil
	}
	return &Client{
		baseURL: cfg.SML.BaseURL,
		apiKey:  cfg.SML.APIKey,
		tenant:  cfg.SML.Tenant,
		http:    &http.Client{Timeout: 10 * time.Second},
	}
}

// Lock calls POST {baseURL}/api/v1/documents/{docNo}/lock.
//
// Return values:
//   - (result, nil)          — success or already_locked (idempotent)
//   - (_, ErrDocNotFound)    — 404: permanent, do NOT retry
//   - (_, err)               — timeout or other error: retryable
func (c *Client) Lock(ctx context.Context, docNo string) (LockResult, error) {
	// Escape doc_no so a value with spaces/'#'/'/'/'%' cannot malform the URL or
	// alter routing. Current doc_no format is alphanumeric+dash, but never trust
	// a path segment built from data.
	url := fmt.Sprintf("%s/api/v1/documents/%s/lock", c.baseURL, urlpkg.PathEscape(docNo))

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return LockResult{}, fmt.Errorf("build request: %w", err)
	}
	// Never log these headers.
	req.Header.Set("X-Api-Key", c.apiKey)
	req.Header.Set("X-Tenant", c.tenant)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		// Includes context deadline (timeout) — retryable.
		return LockResult{}, fmt.Errorf("sml lock request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return LockResult{}, ErrDocNotFound
	}

	if resp.StatusCode != http.StatusOK {
		return LockResult{}, fmt.Errorf("sml lock: unexpected status %d", resp.StatusCode)
	}

	var body lockResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return LockResult{}, fmt.Errorf("sml lock decode: %w", err)
	}
	if !body.Success {
		code := ""
		if body.Error != nil {
			code = body.Error.Code
		}
		return LockResult{}, fmt.Errorf("sml lock: success=false code=%s", code)
	}

	return body.Data, nil
}
