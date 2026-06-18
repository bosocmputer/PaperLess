package db

import (
	"context"
	"os"
	"testing"
	"time"

	"paperless-api/internal/config"
)

// TestNew_AppliesStatementTimeout proves that db.New (the DELIVERED wiring, not a
// re-implementation) actually applies DB_STATEMENT_TIMEOUT_MS to every pooled
// connection. The Step 3b delivery test reimplemented the AfterConnect inline in
// the handlers package, so it did NOT exercise db.New — a regression in the pool
// wiring (e.g. the config field not being read, or AfterConnect not set) would
// pass that test but break production. This test calls db.New directly.
//
// Gated on PAPERLESS_TEST_DB.
func TestNew_AppliesStatementTimeout(t *testing.T) {
	dsn := os.Getenv("PAPERLESS_TEST_DB")
	if dsn == "" {
		t.Skip("PAPERLESS_TEST_DB not set")
	}
	ctx := context.Background()

	cfg := &config.Config{}
	cfg.DB.URL = dsn
	cfg.DB.MaxConns = 4
	cfg.DB.MinConns = 1
	cfg.DB.StatementTimeout = 200 // ms

	pool, err := New(ctx, cfg)
	if err != nil {
		t.Fatalf("db.New: %v", err)
	}
	defer pool.Close()

	start := time.Now()
	_, err = pool.Exec(ctx, "SELECT pg_sleep(5)")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected statement_timeout error from db.New-configured pool, got nil")
	}
	if elapsed > 2*time.Second {
		t.Errorf("query ran %v — db.New did not apply the timeout (want < 2s)", elapsed)
	}
	t.Logf("db.New(timeout=200ms): pg_sleep(5) cut off after %v: %v", elapsed, err)
}

// TestNew_ZeroTimeout_NoCutoff proves the inverse: when StatementTimeout is 0
// (disabled), db.New does NOT install AfterConnect and a query is not cut off by
// a pool-level timeout. We use a short pg_sleep so the test stays fast, and assert
// it completes successfully (no 57014). This guards against accidentally forcing a
// timeout on deployments that explicitly disable it.
func TestNew_ZeroTimeout_NoCutoff(t *testing.T) {
	dsn := os.Getenv("PAPERLESS_TEST_DB")
	if dsn == "" {
		t.Skip("PAPERLESS_TEST_DB not set")
	}
	ctx := context.Background()

	cfg := &config.Config{}
	cfg.DB.URL = dsn
	cfg.DB.MaxConns = 2
	cfg.DB.MinConns = 1
	cfg.DB.StatementTimeout = 0 // disabled

	pool, err := New(ctx, cfg)
	if err != nil {
		t.Fatalf("db.New: %v", err)
	}
	defer pool.Close()

	if _, err := pool.Exec(ctx, "SELECT pg_sleep(0.5)"); err != nil {
		t.Errorf("with timeout disabled, a 0.5s query must succeed, got: %v", err)
	}
}
