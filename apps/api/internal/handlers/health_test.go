package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"paperless-api/internal/config"
	"paperless-api/internal/storage"
)

// newHealthRouter wires /health and /health/ready with the supplied handler.
func newHealthRouter(h *HealthHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/health", h.Live)
	r.GET("/health/ready", h.Ready)
	return r
}

// realMinIOStore returns a storage.Client pointed at MINIO_TEST_ENDPOINT.
// Returns nil if the env var is not set.
func realMinIOStore(t *testing.T) *storage.Client {
	t.Helper()
	endpoint := os.Getenv("MINIO_TEST_ENDPOINT")
	if endpoint == "" {
		return nil
	}
	cfg := &config.Config{}
	cfg.Storage.Endpoint = endpoint
	cfg.Storage.AccessKey = getEnvOr("MINIO_TEST_ACCESS_KEY", "minioadmin")
	cfg.Storage.SecretKey = getEnvOr("MINIO_TEST_SECRET_KEY", "minioadmin")
	cfg.Storage.Bucket = getEnvOr("MINIO_TEST_BUCKET", "paperless")
	cfg.Storage.UseSSL = false
	store, err := storage.New(cfg)
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	return store
}

// deadMinIOStore returns a storage.Client pointed at an unreachable address.
// Used to simulate storage being down.
func deadMinIOStore(t *testing.T) *storage.Client {
	t.Helper()
	cfg := &config.Config{}
	cfg.Storage.Endpoint = "127.0.0.1:19099" // nothing listening here
	cfg.Storage.AccessKey = "x"
	cfg.Storage.SecretKey = "x"
	cfg.Storage.Bucket = "paperless"
	cfg.Storage.UseSSL = false
	store, err := storage.New(cfg)
	if err != nil {
		t.Fatalf("storage.New dead: %v", err)
	}
	return store
}

// missingBucketStore returns a storage.Client pointed at a REAL, reachable MinIO
// but configured with a bucket that does not exist. Returns nil if
// MINIO_TEST_ENDPOINT is not set. This exercises the dangerous failure mode that
// BucketExists reports as (false, nil): MinIO up but the configured bucket gone
// (e.g. restarted with a fresh volume). The service cannot function in this
// state, so Ready MUST report storage=error.
func missingBucketStore(t *testing.T) *storage.Client {
	t.Helper()
	endpoint := os.Getenv("MINIO_TEST_ENDPOINT")
	if endpoint == "" {
		return nil
	}
	cfg := &config.Config{}
	cfg.Storage.Endpoint = endpoint
	cfg.Storage.AccessKey = "minioadmin"
	cfg.Storage.SecretKey = "minioadmin"
	cfg.Storage.Bucket = "nonexistent-bucket-health-test"
	cfg.Storage.UseSSL = false
	store, err := storage.New(cfg)
	if err != nil {
		t.Fatalf("storage.New missing-bucket: %v", err)
	}
	return store
}

// deadDBPool returns a pool pointing at an unreachable host so that Ping fails.
func deadDBPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(context.Background(),
		"postgres://nobody:x@127.0.0.1:19098/nobody?sslmode=disable&connect_timeout=1")
	if err != nil {
		t.Fatalf("dead pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// TestHealth_Live always returns 200 regardless of dependency state.
func TestHealth_Live(t *testing.T) {
	h := NewHealthHandler(deadDBPool(t), deadMinIOStore(t))
	r := newHealthRouter(h)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/health", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("live: got %d, want 200", w.Code)
	}
}

// TestHealth_Ready_BothUp verifies that /health/ready → 200 with database=ok
// and storage=ok when both dependencies are healthy.
// Gated on PAPERLESS_TEST_DB and MINIO_TEST_ENDPOINT.
func TestHealth_Ready_BothUp(t *testing.T) {
	pool := validationPool(t)
	store := realMinIOStore(t)
	if store == nil {
		t.Skip("MINIO_TEST_ENDPOINT not set")
	}
	if err := store.EnsureBucket(context.Background()); err != nil {
		t.Fatalf("ensure bucket: %v", err)
	}

	r := newHealthRouter(NewHealthHandler(pool, store))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/health/ready", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("ready (both up): got %d, want 200 — body: %s", w.Code, w.Body)
	}
	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body["status"] != "ok" {
		t.Errorf("status: got %q, want ok", body["status"])
	}
	if body["database"] != "ok" {
		t.Errorf("database: got %q, want ok", body["database"])
	}
	if body["storage"] != "ok" {
		t.Errorf("storage: got %q, want ok", body["storage"])
	}
}

// TestHealth_Ready_DBDown verifies that /health/ready → 503 with database=error
// when the database is unreachable.
func TestHealth_Ready_DBDown(t *testing.T) {
	// Dead DB pool + no storage check (store=nil skips it).
	r := newHealthRouter(NewHealthHandler(deadDBPool(t), nil))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/health/ready", nil))

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("ready (DB down): got %d, want 503", w.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body["status"] != "error" {
		t.Errorf("status: got %q, want error", body["status"])
	}
	if body["database"] != "error" {
		t.Errorf("database: got %q, want error", body["database"])
	}
}

// TestHealth_Ready_StorageDown verifies that /health/ready → 503 with
// storage=error when MinIO is unreachable (DB is up).
// Gated on PAPERLESS_TEST_DB so we have a real reachable DB to confirm that
// only the storage check drives the failure.
func TestHealth_Ready_StorageDown(t *testing.T) {
	pool := validationPool(t)
	r := newHealthRouter(NewHealthHandler(pool, deadMinIOStore(t)))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/health/ready", nil))

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("ready (storage down): got %d, want 503", w.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body["status"] != "error" {
		t.Errorf("status: got %q, want error", body["status"])
	}
	if body["storage"] != "error" {
		t.Errorf("storage: got %q, want error", body["storage"])
	}
	// DB should still report ok
	if body["database"] != "ok" {
		t.Errorf("database: got %q, want ok (only storage was down)", body["database"])
	}
}

// TestHealth_Ready_StorageBucketMissing is the regression test for the gap the
// delivery missed: MinIO is reachable but the configured bucket does not exist.
// minio's BucketExists returns (false, nil) — NOT an error — in this case, so a
// naive Ping would report storage=ok while every upload/download fails. Ready
// MUST return 503 storage=error. This is the most realistic storage incident:
// MinIO container restarts with a fresh/empty volume.
// Gated on PAPERLESS_TEST_DB (real DB) and MINIO_TEST_ENDPOINT (real MinIO).
func TestHealth_Ready_StorageBucketMissing(t *testing.T) {
	pool := validationPool(t)
	store := missingBucketStore(t)
	if store == nil {
		t.Skip("MINIO_TEST_ENDPOINT not set")
	}

	r := newHealthRouter(NewHealthHandler(pool, store))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/health/ready", nil))

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("ready (bucket missing): got %d, want 503 — body: %s", w.Code, w.Body)
	}
	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body["storage"] != "error" {
		t.Errorf("storage: got %q, want error (bucket missing must be unhealthy)", body["storage"])
	}
	if body["database"] != "ok" {
		t.Errorf("database: got %q, want ok (only bucket was missing)", body["database"])
	}
}
