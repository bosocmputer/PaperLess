package sml_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"paperless-api/internal/config"
	"paperless-api/internal/sml"
)

func newTestClient(t *testing.T, srv *httptest.Server) *sml.Client {
	t.Helper()
	cfg := &config.Config{}
	cfg.SML.BaseURL = srv.URL
	cfg.SML.APIKey = "test-key"
	cfg.SML.Tenant = "test-tenant"
	c := sml.NewClient(cfg)
	if c == nil {
		t.Fatal("NewClient returned nil with non-empty APIKey")
	}
	return c
}

func TestClient_Lock_Success(t *testing.T) {
	var gotAPIKey, gotTenant string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAPIKey = r.Header.Get("X-Api-Key")
		gotTenant = r.Header.Get("X-Tenant")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"success":true,"data":{"doc_no":"PO-001","table":"ic_trans","trans_flag":6,"is_lock_record":1,"already_locked":false}}`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	result, err := c.Lock(context.Background(), "PO-001")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.DocNo != "PO-001" {
		t.Errorf("doc_no: want PO-001, got %s", result.DocNo)
	}
	if result.IsLockRecord != 1 {
		t.Errorf("is_lock_record: want 1, got %d", result.IsLockRecord)
	}
	if gotAPIKey != "test-key" {
		t.Errorf("X-Api-Key header: want test-key, got %s", gotAPIKey)
	}
	if gotTenant != "test-tenant" {
		t.Errorf("X-Tenant header: want test-tenant, got %s", gotTenant)
	}
}

func TestClient_Lock_AlreadyLocked(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"success":true,"data":{"doc_no":"PO-001","table":"ic_trans","trans_flag":6,"is_lock_record":1,"already_locked":true}}`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	result, err := c.Lock(context.Background(), "PO-001")
	if err != nil {
		t.Fatalf("already_locked should still be success, got error: %v", err)
	}
	if !result.AlreadyLocked {
		t.Error("want AlreadyLocked=true")
	}
}

func TestClient_Lock_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"success":false,"error":{"code":"document_not_found"}}`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	_, err := c.Lock(context.Background(), "MISSING-001")
	if !errors.Is(err, sml.ErrDocNotFound) {
		t.Errorf("want ErrDocNotFound, got %v", err)
	}
}

func TestClient_Lock_ServerError_Retryable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	_, err := c.Lock(context.Background(), "PO-001")
	if err == nil {
		t.Fatal("want error for 500, got nil")
	}
	if errors.Is(err, sml.ErrDocNotFound) {
		t.Error("500 should not be ErrDocNotFound")
	}
}

func TestClient_Lock_Timeout_Retryable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Block until client times out.
		select {
		case <-r.Context().Done():
		case <-time.After(30 * time.Second):
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := &config.Config{}
	cfg.SML.BaseURL = srv.URL
	cfg.SML.APIKey = "test-key"
	cfg.SML.Tenant = "test-tenant"

	// Use a very short context deadline to simulate timeout.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	c := sml.NewClient(cfg)
	_, err := c.Lock(ctx, "PO-001")
	if err == nil {
		t.Fatal("want timeout error, got nil")
	}
	if errors.Is(err, sml.ErrDocNotFound) {
		t.Error("timeout should not be ErrDocNotFound")
	}
}

func TestNewClient_NilWhenNoAPIKey(t *testing.T) {
	cfg := &config.Config{}
	cfg.SML.BaseURL = "http://localhost:8200"
	cfg.SML.APIKey = ""
	c := sml.NewClient(cfg)
	if c != nil {
		t.Error("want nil client when APIKey is empty")
	}
}
