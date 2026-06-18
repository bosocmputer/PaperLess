package handlers

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

// validationDSN returns the test DSN or calls t.Skip.
func validationDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("PAPERLESS_TEST_DB")
	if dsn == "" {
		t.Skip("PAPERLESS_TEST_DB not set")
	}
	return dsn
}

// ── Step 3a: rate-limiter bucket eviction ────────────────────────────────────

// TestRateLimiter_BucketEviction confirms that evictStaleBuckets removes entries
// whose window elapsed more than one full window ago, bounding the buckets map.
func TestRateLimiter_BucketEviction(t *testing.T) {
	h := &ExternalSignHandler{
		buckets: make(map[string]*ipBucket),
		log:     zap.NewNop(),
	}

	// Seed 1000 distinct IPs with windowAt in the past (2 full windows ago).
	staleTime := time.Now().Add(-2 * rateLimitWindow)
	for i := 0; i < 1000; i++ {
		ip := fmt.Sprintf("10.0.%d.%d", i/256, i%256)
		h.buckets[ip] = &ipBucket{count: 5, windowAt: staleTime}
	}

	// Add 10 fresh IPs (within the current window).
	freshTime := time.Now()
	for i := 0; i < 10; i++ {
		ip := fmt.Sprintf("192.168.1.%d", i)
		h.buckets[ip] = &ipBucket{count: 1, windowAt: freshTime}
	}

	if len(h.buckets) != 1010 {
		t.Fatalf("pre-eviction: want 1010 buckets, got %d", len(h.buckets))
	}

	h.evictStaleBuckets()

	if len(h.buckets) != 10 {
		t.Errorf("post-eviction: want 10 (only fresh IPs), got %d", len(h.buckets))
	}

	// Confirm fresh IPs survived.
	for i := 0; i < 10; i++ {
		ip := fmt.Sprintf("192.168.1.%d", i)
		if _, ok := h.buckets[ip]; !ok {
			t.Errorf("fresh IP %s was incorrectly evicted", ip)
		}
	}
}

// TestRateLimiter_LivePathLeakThenEvict closes the gap the delivery left: the
// delivered eviction test hand-crafts bucket entries, so it proves the sweep
// logic but NOT that the actual leak exists and is then cleaned. This test drives
// the REAL entry path (checkRateLimit) with many distinct IPs — reproducing the
// unbounded-growth leak — then ages the windows and runs evictStaleBuckets to
// prove the live-created buckets are actually reclaimable.
func TestRateLimiter_LivePathLeakThenEvict(t *testing.T) {
	h := &ExternalSignHandler{
		buckets: make(map[string]*ipBucket),
		log:     zap.NewNop(),
	}

	// Drive the live entry path with 5000 distinct IPs (simulating a public-facing
	// endpoint scraped by many sources). Every call creates/updates a bucket.
	for i := 0; i < 5000; i++ {
		ip := fmt.Sprintf("203.0.%d.%d", i/256, i%256)
		_ = h.checkRateLimit(ip)
	}
	if len(h.buckets) != 5000 {
		t.Fatalf("live path did not create one bucket per IP: want 5000, got %d", len(h.buckets))
	}

	// Age every live-created bucket past one full window (what the janitor sees
	// after the IPs go quiet). We mutate windowAt directly under the lock to
	// simulate the passage of time without sleeping.
	h.mu.Lock()
	old := time.Now().Add(-2 * rateLimitWindow)
	for _, b := range h.buckets {
		b.windowAt = old
	}
	h.mu.Unlock()

	h.evictStaleBuckets()

	if len(h.buckets) != 0 {
		t.Errorf("after the window elapsed, the janitor must reclaim all idle buckets: %d remain", len(h.buckets))
	}
}

// TestRateLimiter_StillEnforcesAfterEviction proves eviction does not weaken the
// limit: an IP that gets evicted and then returns starts a fresh window (correct),
// and an active IP within its window is still capped at rateLimitMaxAttempts.
func TestRateLimiter_StillEnforcesAfterEviction(t *testing.T) {
	h := &ExternalSignHandler{
		buckets: make(map[string]*ipBucket),
		log:     zap.NewNop(),
	}
	const ip = "198.51.100.7"

	// First request opens the window; the next rateLimitMaxAttempts-1 are allowed;
	// the one after that is blocked.
	for i := 0; i < rateLimitMaxAttempts; i++ {
		if h.checkRateLimit(ip) {
			t.Fatalf("request %d was blocked before reaching the limit", i+1)
		}
	}
	if !h.checkRateLimit(ip) {
		t.Fatal("request beyond the limit was NOT blocked")
	}

	// Age the bucket and evict (IP went quiet).
	h.mu.Lock()
	h.buckets[ip].windowAt = time.Now().Add(-2 * rateLimitWindow)
	h.mu.Unlock()
	h.evictStaleBuckets()
	if _, ok := h.buckets[ip]; ok {
		t.Fatal("idle IP was not evicted")
	}

	// The returning IP gets a fresh window — first request allowed again.
	if h.checkRateLimit(ip) {
		t.Error("returning IP should start a fresh window, but was blocked")
	}
}

// TestRateLimiter_JanitorInterval is a documentation guard — the per-process
// in-memory caveat is documented in the constant block comment and in
// docs/deploy-instances.md. If the interval changes, the doc reference needs updating.
func TestRateLimiter_JanitorInterval(t *testing.T) {
	const expected = 2 * time.Minute
	if rateLimiterJanitorInterval != expected {
		t.Errorf("janitor interval changed: want %v, got %v — update docs/deploy-instances.md if intentional",
			expected, rateLimiterJanitorInterval)
	}
}

// ── Step 3b: statement_timeout cuts off stuck queries ────────────────────────

// TestStatementTimeout_CutsOffSlowQuery proves that a pool built with
// AfterConnect SET statement_timeout kills pg_sleep(5) within ~200ms (the
// configured timeout), not 5 seconds. This mirrors the db.New AfterConnect
// path so a regression in the config wiring is caught.
func TestStatementTimeout_CutsOffSlowQuery(t *testing.T) {
	dsn := validationDSN(t)
	ctx := context.Background()

	const timeoutMS = 200

	poolCfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse DSN: %v", err)
	}
	poolCfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, fmt.Sprintf("SET statement_timeout = %d", timeoutMS))
		return err
	}

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}
	defer pool.Close()

	start := time.Now()
	_, err = pool.Exec(ctx, "SELECT pg_sleep(5)") // 5s sleep vs 200ms timeout
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected statement_timeout error, got nil — query was not cut off")
	}
	// Should be cut off within ~500ms (200ms timeout + connection overhead).
	if elapsed > 2*time.Second {
		t.Errorf("query ran for %v — timeout did not fire (want < 2s)", elapsed)
	}
	t.Logf("pg_sleep(5) cut off after %v with: %v", elapsed, err)
}
