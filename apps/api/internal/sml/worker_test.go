package sml_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"paperless-api/internal/config"
	"paperless-api/internal/sml"
)

func testDBWorker(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("PAPERLESS_TEST_DB")
	if dsn == "" {
		t.Skip("PAPERLESS_TEST_DB not set — skipping DB integration tests")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("open test pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// seedDocAndJob inserts a minimal document + one update_lock sml_sync_job and
// returns docID + jobID. Cleans up on t.Cleanup.
func seedDocAndJob(t *testing.T, pool *pgxpool.Pool, docNo string) (docID, jobID int64) {
	t.Helper()
	ctx := context.Background()

	// Need a valid workflow_template_id — use the POP template from seed data.
	var templateID int64
	var templateVersion int
	if err := pool.QueryRow(ctx,
		`SELECT id, version FROM workflow_templates WHERE doc_format_code='POP' AND status='active'`,
	).Scan(&templateID, &templateVersion); err != nil {
		t.Fatalf("find POP template: %v", err)
	}

	idKey := fmt.Sprintf("SML:WORKER:%s:%d", docNo, time.Now().UnixNano())
	if err := pool.QueryRow(ctx, `
		INSERT INTO documents (doc_format_code, doc_no, revision, workflow_template_id, workflow_version,
		                       status, sync_status, idempotency_key, source_hash)
		VALUES ('POP', $1, 0, $2, $3, 'completed', 'sync_pending', $4, 'testhash')
		RETURNING id
	`, docNo, templateID, templateVersion, idKey).Scan(&docID); err != nil {
		t.Fatalf("insert doc: %v", err)
	}

	if err := pool.QueryRow(ctx, `
		INSERT INTO sml_sync_jobs (document_id, job_type, status, max_attempts)
		VALUES ($1, 'update_lock', 'pending', 5)
		RETURNING id
	`, docID).Scan(&jobID); err != nil {
		t.Fatalf("insert job: %v", err)
	}

	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = pool.Exec(ctx, `DELETE FROM sml_sync_jobs WHERE document_id=$1`, docID)
		_, _ = pool.Exec(ctx, `DELETE FROM audit_logs WHERE entity_type='sync' AND entity_id=$1`,
			strconv.FormatInt(docID, 10))
		_, _ = pool.Exec(ctx, `DELETE FROM documents WHERE id=$1`, docID)
	})
	return
}

func newWorkerWithServer(t *testing.T, pool *pgxpool.Pool, srv *httptest.Server) *sml.Worker {
	t.Helper()
	cfg := &config.Config{}
	cfg.SML.BaseURL = srv.URL
	cfg.SML.APIKey = "test-key"
	cfg.SML.Tenant = "test-tenant"
	client := sml.NewClient(cfg)
	log, _ := zap.NewDevelopment()
	return sml.NewWorker(pool, client, log, 10*time.Millisecond)
}

func TestWorker_Success(t *testing.T) {
	pool := testDBWorker(t)
	docNo := fmt.Sprintf("WRK-OK-%d", time.Now().UnixNano())
	docID, jobID := seedDocAndJob(t, pool, docNo)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"success":true,"data":{"doc_no":%q,"table":"ic_trans","trans_flag":6,"is_lock_record":1,"already_locked":false}}`, docNo)
	}))
	defer srv.Close()

	worker := newWorkerWithServer(t, pool, srv)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	go worker.Run(ctx)

	// Wait for job to reach succeeded.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var status string
		_ = pool.QueryRow(context.Background(),
			`SELECT status FROM sml_sync_jobs WHERE id=$1`, jobID).Scan(&status)
		if status == "succeeded" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	var jobStatus string
	if err := pool.QueryRow(context.Background(),
		`SELECT status FROM sml_sync_jobs WHERE id=$1`, jobID).Scan(&jobStatus); err != nil {
		t.Fatalf("read job status: %v", err)
	}
	if jobStatus != "succeeded" {
		t.Errorf("job status: want succeeded, got %s", jobStatus)
	}

	var syncStatus string
	if err := pool.QueryRow(context.Background(),
		`SELECT sync_status FROM documents WHERE id=$1`, docID).Scan(&syncStatus); err != nil {
		t.Fatalf("read sync_status: %v", err)
	}
	if syncStatus != "synced" {
		t.Errorf("sync_status: want synced, got %s", syncStatus)
	}

	var auditCount int
	_ = pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_logs WHERE entity_type='sync' AND entity_id=$1 AND action='document_synced'`,
		strconv.FormatInt(docID, 10)).Scan(&auditCount)
	if auditCount != 1 {
		t.Errorf("audit_logs: want 1 document_synced entry, got %d", auditCount)
	}
}

func TestWorker_DocNotFound_PermanentFailure(t *testing.T) {
	pool := testDBWorker(t)
	docNo := fmt.Sprintf("WRK-NF-%d", time.Now().UnixNano())
	docID, jobID := seedDocAndJob(t, pool, docNo)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"success":false,"error":{"code":"document_not_found"}}`))
	}))
	defer srv.Close()

	worker := newWorkerWithServer(t, pool, srv)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	go worker.Run(ctx)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var status string
		_ = pool.QueryRow(context.Background(),
			`SELECT status FROM sml_sync_jobs WHERE id=$1`, jobID).Scan(&status)
		if status == "failed" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	var jobStatus string
	_ = pool.QueryRow(context.Background(),
		`SELECT status FROM sml_sync_jobs WHERE id=$1`, jobID).Scan(&jobStatus)
	if jobStatus != "failed" {
		t.Errorf("job status: want failed, got %s", jobStatus)
	}

	// Verify no retry scheduled (attempt_count should be 1, no retry row).
	var attemptCount int
	_ = pool.QueryRow(context.Background(),
		`SELECT attempt_count FROM sml_sync_jobs WHERE id=$1`, jobID).Scan(&attemptCount)
	if attemptCount != 1 {
		t.Errorf("attempt_count: want 1, got %d", attemptCount)
	}

	var syncStatus string
	_ = pool.QueryRow(context.Background(),
		`SELECT sync_status FROM documents WHERE id=$1`, docID).Scan(&syncStatus)
	if syncStatus != "sync_failed" {
		t.Errorf("sync_status: want sync_failed, got %s", syncStatus)
	}
}

func TestWorker_RetryableError_ExhaustsMaxAttempts(t *testing.T) {
	pool := testDBWorker(t)
	docNo := fmt.Sprintf("WRK-RETRY-%d", time.Now().UnixNano())

	// Seed with max_attempts=1 so one failure exhausts it immediately.
	var templateID int64
	var templateVersion int
	ctx := context.Background()
	_ = pool.QueryRow(ctx,
		`SELECT id, version FROM workflow_templates WHERE doc_format_code='POP' AND status='active'`,
	).Scan(&templateID, &templateVersion)

	idKey := fmt.Sprintf("SML:RETRY:%s:%d", docNo, time.Now().UnixNano())
	var docID, jobID int64
	_ = pool.QueryRow(ctx, `
		INSERT INTO documents (doc_format_code, doc_no, revision, workflow_template_id, workflow_version,
		                       status, sync_status, idempotency_key, source_hash)
		VALUES ('POP', $1, 0, $2, $3, 'completed', 'sync_pending', $4, 'testhash')
		RETURNING id
	`, docNo, templateID, templateVersion, idKey).Scan(&docID)
	_ = pool.QueryRow(ctx, `
		INSERT INTO sml_sync_jobs (document_id, job_type, status, max_attempts)
		VALUES ($1, 'update_lock', 'pending', 1)
		RETURNING id
	`, docID).Scan(&jobID)

	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = pool.Exec(ctx, `DELETE FROM sml_sync_jobs WHERE document_id=$1`, docID)
		_, _ = pool.Exec(ctx, `DELETE FROM audit_logs WHERE entity_type='sync' AND entity_id=$1`,
			strconv.FormatInt(docID, 10))
		_, _ = pool.Exec(ctx, `DELETE FROM documents WHERE id=$1`, docID)
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	worker := newWorkerWithServer(t, pool, srv)
	wCtx, wCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer wCancel()
	go worker.Run(wCtx)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var status string
		_ = pool.QueryRow(context.Background(),
			`SELECT status FROM sml_sync_jobs WHERE id=$1`, jobID).Scan(&status)
		if status == "failed" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	var jobStatus string
	_ = pool.QueryRow(context.Background(),
		`SELECT status FROM sml_sync_jobs WHERE id=$1`, jobID).Scan(&jobStatus)
	if jobStatus != "failed" {
		t.Errorf("job status after exhausting max_attempts=1: want failed, got %s", jobStatus)
	}

	var syncStatus string
	_ = pool.QueryRow(context.Background(),
		`SELECT sync_status FROM documents WHERE id=$1`, docID).Scan(&syncStatus)
	if syncStatus != "sync_failed" {
		t.Errorf("sync_status: want sync_failed, got %s", syncStatus)
	}
}

func TestWorker_RetryableError_SetsRetryStatus(t *testing.T) {
	pool := testDBWorker(t)
	docNo := fmt.Sprintf("WRK-RET2-%d", time.Now().UnixNano())

	// max_attempts=5 so first failure → retry status, not failed.
	_, jobID := seedDocAndJob(t, pool, docNo)

	var callCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&callCount, 1) == 1 {
			// First call fails.
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		// Subsequent calls succeed (won't be reached in this test window).
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"success":true,"data":{"doc_no":%q,"table":"ic_trans","trans_flag":6,"is_lock_record":1,"already_locked":false}}`, docNo)
	}))
	defer srv.Close()

	worker := newWorkerWithServer(t, pool, srv)
	wCtx, wCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer wCancel()
	go worker.Run(wCtx)

	// Wait until first attempt is processed (status becomes retry).
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var status string
		_ = pool.QueryRow(context.Background(),
			`SELECT status FROM sml_sync_jobs WHERE id=$1`, jobID).Scan(&status)
		if status == "retry" || status == "succeeded" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	var jobStatus string
	_ = pool.QueryRow(context.Background(),
		`SELECT status FROM sml_sync_jobs WHERE id=$1`, jobID).Scan(&jobStatus)
	if jobStatus != "retry" && jobStatus != "succeeded" {
		t.Errorf("job status after one retryable failure: want retry or succeeded, got %s", jobStatus)
	}

	// If it's retry, verify next_retry_at is set in the future.
	if jobStatus == "retry" {
		var nextRetryAt *time.Time
		_ = pool.QueryRow(context.Background(),
			`SELECT next_retry_at FROM sml_sync_jobs WHERE id=$1`, jobID).Scan(&nextRetryAt)
		if nextRetryAt == nil || nextRetryAt.Before(time.Now()) {
			t.Error("next_retry_at should be set in the future for retry status")
		}
	}
}

func TestWorker_SkipLocked_TwoWorkersSingleJob(t *testing.T) {
	pool := testDBWorker(t)
	docNo := fmt.Sprintf("WRK-SKIP-%d", time.Now().UnixNano())
	_, jobID := seedDocAndJob(t, pool, docNo)

	// Count how many times the lock endpoint was called.
	var callCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&callCount, 1)
		// Add a small delay so two workers have time to try to claim the job.
		time.Sleep(50 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"success":true,"data":{"doc_no":%q,"table":"ic_trans","trans_flag":6,"is_lock_record":1,"already_locked":false}}`, docNo)
	}))
	defer srv.Close()

	cfg := &config.Config{}
	cfg.SML.BaseURL = srv.URL
	cfg.SML.APIKey = "test-key"
	cfg.SML.Tenant = "test-tenant"
	log, _ := zap.NewDevelopment()

	var wg sync.WaitGroup
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// Launch two workers concurrently.
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := sml.NewWorker(pool, sml.NewClient(cfg), log, 10*time.Millisecond)
			w.Run(ctx)
		}()
	}

	// Wait until the job is done.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var status string
		_ = pool.QueryRow(context.Background(),
			`SELECT status FROM sml_sync_jobs WHERE id=$1`, jobID).Scan(&status)
		if status == "succeeded" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	wg.Wait()

	if n := atomic.LoadInt32(&callCount); n != 1 {
		t.Errorf("lock endpoint called %d times, want exactly 1 (SKIP LOCKED)", n)
	}
}

// TestWorker_StaleRunning_Recovered proves a job stranded in 'running' (worker
// crashed between marking running and recording the outcome) is re-claimed and
// driven to a terminal state — not lost. We simulate the strand by inserting a
// 'running' job whose updated_at is older than runningTimeout.
func TestWorker_StaleRunning_Recovered(t *testing.T) {
	pool := testDBWorker(t)
	docNo := fmt.Sprintf("WRK-STALE-%d", time.Now().UnixNano())
	ctx := context.Background()

	var templateID int64
	var templateVersion int
	_ = pool.QueryRow(ctx,
		`SELECT id, version FROM workflow_templates WHERE doc_format_code='POP' AND status='active'`,
	).Scan(&templateID, &templateVersion)

	idKey := fmt.Sprintf("SML:STALE:%s:%d", docNo, time.Now().UnixNano())
	var docID, jobID int64
	_ = pool.QueryRow(ctx, `
		INSERT INTO documents (doc_format_code, doc_no, revision, workflow_template_id, workflow_version,
		                       status, sync_status, idempotency_key, source_hash)
		VALUES ('POP', $1, 0, $2, $3, 'completed', 'sync_pending', $4, 'testhash')
		RETURNING id
	`, docNo, templateID, templateVersion, idKey).Scan(&docID)

	// Insert a job stuck 'running' with updated_at 10 minutes ago (> 2m timeout).
	_ = pool.QueryRow(ctx, `
		INSERT INTO sml_sync_jobs (document_id, job_type, status, max_attempts, updated_at)
		VALUES ($1, 'update_lock', 'running', 5, now() - interval '10 minutes')
		RETURNING id
	`, docID).Scan(&jobID)

	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = pool.Exec(ctx, `DELETE FROM sml_sync_jobs WHERE document_id=$1`, docID)
		_, _ = pool.Exec(ctx, `DELETE FROM audit_logs WHERE entity_type='sync' AND entity_id=$1`,
			strconv.FormatInt(docID, 10))
		_, _ = pool.Exec(ctx, `DELETE FROM documents WHERE id=$1`, docID)
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"success":true,"data":{"doc_no":%q,"table":"ic_trans","trans_flag":6,"is_lock_record":1,"already_locked":false}}`, docNo)
	}))
	defer srv.Close()

	worker := newWorkerWithServer(t, pool, srv)
	wCtx, wCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer wCancel()
	go worker.Run(wCtx)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var status string
		_ = pool.QueryRow(context.Background(),
			`SELECT status FROM sml_sync_jobs WHERE id=$1`, jobID).Scan(&status)
		if status == "succeeded" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	var jobStatus string
	_ = pool.QueryRow(context.Background(),
		`SELECT status FROM sml_sync_jobs WHERE id=$1`, jobID).Scan(&jobStatus)
	if jobStatus != "succeeded" {
		t.Errorf("stale running job: want recovered to succeeded, got %s", jobStatus)
	}
}

// TestWorker_FreshRunning_NotStolen proves a job marked 'running' recently (a
// healthy in-flight attempt by another worker) is NOT re-claimed by this worker.
func TestWorker_FreshRunning_NotStolen(t *testing.T) {
	pool := testDBWorker(t)
	docNo := fmt.Sprintf("WRK-FRESH-%d", time.Now().UnixNano())
	ctx := context.Background()

	var templateID int64
	var templateVersion int
	_ = pool.QueryRow(ctx,
		`SELECT id, version FROM workflow_templates WHERE doc_format_code='POP' AND status='active'`,
	).Scan(&templateID, &templateVersion)

	idKey := fmt.Sprintf("SML:FRESH:%s:%d", docNo, time.Now().UnixNano())
	var docID, jobID int64
	_ = pool.QueryRow(ctx, `
		INSERT INTO documents (doc_format_code, doc_no, revision, workflow_template_id, workflow_version,
		                       status, sync_status, idempotency_key, source_hash)
		VALUES ('POP', $1, 0, $2, $3, 'completed', 'sync_pending', $4, 'testhash')
		RETURNING id
	`, docNo, templateID, templateVersion, idKey).Scan(&docID)

	// 'running' updated just now — must NOT be stolen (within the 2m window).
	_ = pool.QueryRow(ctx, `
		INSERT INTO sml_sync_jobs (document_id, job_type, status, max_attempts, updated_at)
		VALUES ($1, 'update_lock', 'running', 5, now())
		RETURNING id
	`, docID).Scan(&jobID)

	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = pool.Exec(ctx, `DELETE FROM sml_sync_jobs WHERE document_id=$1`, docID)
		_, _ = pool.Exec(ctx, `DELETE FROM documents WHERE id=$1`, docID)
	})

	var called int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&called, 1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"success":true,"data":{"doc_no":%q,"table":"ic_trans","trans_flag":6,"is_lock_record":1,"already_locked":false}}`, docNo)
	}))
	defer srv.Close()

	worker := newWorkerWithServer(t, pool, srv)
	wCtx, wCancel := context.WithTimeout(context.Background(), 800*time.Millisecond)
	defer wCancel()
	worker.Run(wCtx) // run until the short ctx expires

	if n := atomic.LoadInt32(&called); n != 0 {
		t.Errorf("fresh running job was stolen: lock endpoint called %d times, want 0", n)
	}
	var jobStatus string
	_ = pool.QueryRow(context.Background(),
		`SELECT status FROM sml_sync_jobs WHERE id=$1`, jobID).Scan(&jobStatus)
	if jobStatus != "running" {
		t.Errorf("fresh running job status changed: want running, got %s", jobStatus)
	}
}
