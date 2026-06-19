package sml

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

// backoffDurations maps attempt_count (1-based, after increment) to the delay
// before the next retry. Bounded at 5 attempts (index 0 = after 1st failure).
var backoffDurations = []time.Duration{
	30 * time.Second,
	1 * time.Minute,
	2 * time.Minute,
	5 * time.Minute,
	15 * time.Minute,
}

func backoff(attemptCount int) time.Duration {
	idx := attemptCount - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(backoffDurations) {
		idx = len(backoffDurations) - 1
	}
	return backoffDurations[idx]
}

// runningTimeout is how long a job may sit in 'running' before another worker
// treats it as abandoned (process crashed/restarted between claiming the job and
// recording its outcome) and re-claims it. Must be safely larger than the client
// HTTP timeout (10s) plus DB round-trips so a healthy in-flight attempt is never
// stolen. 2 minutes gives wide margin while bounding how long a stranded job
// blocks SML sync after a crash.
const runningTimeout = 2 * time.Minute

// Worker polls sml_sync_jobs and calls the SML lock endpoint for each due job.
type Worker struct {
	pool     *pgxpool.Pool
	client   *Client
	log      *zap.Logger
	interval time.Duration
}

// NewWorker constructs a Worker. interval controls how often the ticker fires
// (e.g. 5 * time.Second for production, shorter in tests).
func NewWorker(pool *pgxpool.Pool, client *Client, log *zap.Logger, interval time.Duration) *Worker {
	return &Worker{
		pool:     pool,
		client:   client,
		log:      log,
		interval: interval,
	}
}

// Run starts the polling loop. It returns when ctx is cancelled.
// Each tick claims at most one due job to keep the loop simple and safe.
func (w *Worker) Run(ctx context.Context) {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := w.processTick(ctx); err != nil && !errors.Is(err, context.Canceled) {
				w.log.Error("sml worker tick error", zap.Error(err))
			}
		}
	}
}

func (w *Worker) processTick(ctx context.Context) (retErr error) {
	// Recover so a panic in one tick can't crash the whole process.
	defer func() {
		if r := recover(); r != nil {
			retErr = fmt.Errorf("sml worker panic: %v", r)
		}
	}()

	tx, err := w.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// Claim one due job. SKIP LOCKED is correct here — each job is independent,
	// so skipping a job held by another worker is safe (unlike the engine).
	//
	// Stale-'running' recovery: a job left 'running' longer than runningTimeout
	// means the worker that claimed it crashed/restarted before recording an
	// outcome. Such jobs are re-claimable; otherwise a completed document would
	// never be locked in SML and never retry (silent data-integrity gap). A
	// healthy in-flight attempt holds the row lock via FOR UPDATE, so SKIP LOCKED
	// keeps us from stealing it before the timeout — and the timeout (2m) is far
	// larger than a real attempt (≤10s), so we never steal a live one.
	var jobID int64
	var documentID int64
	var attemptCount int
	var maxAttempts int
	err = tx.QueryRow(ctx, `
		SELECT id, document_id, attempt_count, max_attempts
		  FROM sml_sync_jobs
		 WHERE job_type = 'update_lock'
		   AND (
		         (status IN ('pending','retry')
		          AND (next_retry_at IS NULL OR next_retry_at <= now()))
		      OR (status = 'running' AND updated_at < now() - make_interval(secs => $1)))
		 ORDER BY id
		 LIMIT 1
		   FOR UPDATE SKIP LOCKED
	`, runningTimeout.Seconds()).Scan(&jobID, &documentID, &attemptCount, &maxAttempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // nothing to process
	}
	if err != nil {
		return fmt.Errorf("claim job: %w", err)
	}

	// Mark running.
	if _, err := tx.Exec(ctx,
		`UPDATE sml_sync_jobs SET status='running', updated_at=now() WHERE id=$1`, jobID,
	); err != nil {
		return fmt.Errorf("mark running: %w", err)
	}

	// Look up doc_no.
	var docNo string
	if err := tx.QueryRow(ctx,
		`SELECT doc_no FROM documents WHERE id=$1`, documentID,
	).Scan(&docNo); err != nil {
		return fmt.Errorf("lookup doc_no for document %s: %w", strconv.FormatInt(documentID, 10), err)
	}

	// Must commit the running status before calling the external API so that
	// another worker won't claim the same job while we hold the lock.
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit running status: %w", err)
	}

	// Call SML lock endpoint outside any DB transaction.
	_, lockErr := w.client.Lock(ctx, docNo)

	// Open a new transaction to record the outcome.
	tx2, err := w.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin outcome tx: %w", err)
	}
	defer tx2.Rollback(ctx) //nolint:errcheck

	newAttemptCount := attemptCount + 1

	switch {
	case lockErr == nil:
		// Success (includes already_locked).
		if _, err := tx2.Exec(ctx,
			`UPDATE sml_sync_jobs SET status='succeeded', attempt_count=$2, error_message=NULL, updated_at=now() WHERE id=$1`,
			jobID, newAttemptCount,
		); err != nil {
			return fmt.Errorf("mark succeeded: %w", err)
		}
		if _, err := tx2.Exec(ctx,
			`UPDATE documents SET sync_status='synced', updated_at=now() WHERE id=$1`, documentID,
		); err != nil {
			return fmt.Errorf("update sync_status synced: %w", err)
		}
		if _, err := tx2.Exec(ctx, `
			INSERT INTO audit_logs (actor_type, actor_id, action, entity_type, entity_id)
			VALUES ('system', '0', 'document_synced', 'sync', $1)
		`, strconv.FormatInt(documentID, 10)); err != nil {
			return fmt.Errorf("audit document_synced: %w", err)
		}
		w.log.Info("sml lock succeeded",
			zap.Int64("job_id", jobID),
			zap.Int64("document_id", documentID),
			zap.Int("attempt", newAttemptCount),
		)

	case errors.Is(lockErr, ErrDocNotFound):
		// Permanent failure — doc not in SML; do not retry.
		if _, err := tx2.Exec(ctx,
			`UPDATE sml_sync_jobs SET status='failed', attempt_count=$2, error_message=$3, updated_at=now() WHERE id=$1`,
			jobID, newAttemptCount, "document_not_found",
		); err != nil {
			return fmt.Errorf("mark failed (not found): %w", err)
		}
		if _, err := tx2.Exec(ctx,
			`UPDATE documents SET sync_status='sync_failed', updated_at=now() WHERE id=$1`, documentID,
		); err != nil {
			return fmt.Errorf("update sync_status failed: %w", err)
		}
		w.log.Warn("sml lock permanent failure: doc not found in SML",
			zap.Int64("job_id", jobID),
			zap.Int64("document_id", documentID),
		)

	default:
		// Retryable error (timeout, 5xx, etc.).
		errMsg := lockErr.Error()
		if newAttemptCount >= maxAttempts {
			// Exhausted — mark failed.
			if _, err := tx2.Exec(ctx,
				`UPDATE sml_sync_jobs SET status='failed', attempt_count=$2, error_message=$3, updated_at=now() WHERE id=$1`,
				jobID, newAttemptCount, errMsg,
			); err != nil {
				return fmt.Errorf("mark failed (exhausted): %w", err)
			}
			if _, err := tx2.Exec(ctx,
				`UPDATE documents SET sync_status='sync_failed', updated_at=now() WHERE id=$1`, documentID,
			); err != nil {
				return fmt.Errorf("update sync_status failed (exhausted): %w", err)
			}
			w.log.Error("sml lock exhausted attempts",
				zap.Int64("job_id", jobID),
				zap.Int64("document_id", documentID),
				zap.Int("attempt", newAttemptCount),
				zap.Int("max_attempts", maxAttempts),
				zap.String("error_category", "retryable_exhausted"),
			)
		} else {
			nextRetry := time.Now().Add(backoff(newAttemptCount))
			if _, err := tx2.Exec(ctx,
				`UPDATE sml_sync_jobs SET status='retry', attempt_count=$2, next_retry_at=$3, error_message=$4, updated_at=now() WHERE id=$1`,
				jobID, newAttemptCount, nextRetry, errMsg,
			); err != nil {
				return fmt.Errorf("mark retry: %w", err)
			}
			w.log.Warn("sml lock retryable error",
				zap.Int64("job_id", jobID),
				zap.Int64("document_id", documentID),
				zap.Int("attempt", newAttemptCount),
				zap.String("error_category", "retryable"),
			)
		}
	}

	return tx2.Commit(ctx)
}
