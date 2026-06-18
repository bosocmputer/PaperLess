package workflow_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"paperless-api/internal/workflow"
)

// TestSign_TrueRace_DuplicateRequestID_OneEvent forces many concurrent
// goroutines through the SAME task with the SAME request_id. The app-level
// pre-check SELECT and the event INSERT race, so multiple goroutines pass the
// pre-check and collide on the partial unique index uq_sig_events_request,
// exercising the 23505 idempotent-success path in engine.Sign.
//
// Invariants (proven against a real DB):
//   - exactly ONE signature_event is written
//   - no goroutine returns an unexpected (5xx-class) error — only nil or
//     ErrStepAlreadyActioned
//
// This is the DB-level backstop the Phase 3 plan (Step 1a) requires: the app
// guard alone is not atomic; the unique index is.
func TestSign_TrueRace_DuplicateRequestID_OneEvent(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()
	engine := workflow.New(pool)

	var templateID, makerStepID int64
	_ = pool.QueryRow(ctx, `SELECT id FROM workflow_templates WHERE doc_format_code='POP' AND status='active'`).Scan(&templateID)
	_ = pool.QueryRow(ctx, `SELECT id FROM workflow_steps WHERE workflow_template_id=$1 AND position_code='MAKER'`, templateID).Scan(&makerStepID)
	makerID := userID(t, pool, "maker")

	var docID, taskID int64
	idKey := fmt.Sprintf("POP:RIDTRACE-%d:0", time.Now().UnixNano())
	_ = pool.QueryRow(ctx, `
		INSERT INTO documents (doc_format_code, doc_no, revision, workflow_template_id, workflow_version, status, idempotency_key, source_hash)
		VALUES ('POP', $1, 0, $2, 1, 'pending', $3, 'ridtracehash') RETURNING id
	`, fmt.Sprintf("RIDTRACE-%d", time.Now().UnixNano()), templateID, idKey).Scan(&docID)
	t.Cleanup(func() { cleanupDoc(pool, docID) })

	_ = pool.QueryRow(ctx, `
		INSERT INTO signature_tasks (document_id, workflow_step_id, assigned_user_id, sequence_no, condition_type, status, opened_at)
		VALUES ($1, $2, $3, 1, 1, 'open', now()) RETURNING id
	`, docID, makerStepID, makerID).Scan(&taskID)

	const n = 20
	var wg sync.WaitGroup
	errs := make([]error, n)
	start := make(chan struct{})
	wg.Add(n)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			defer wg.Done()
			<-start // release all at once to maximize contention
			errs[i] = engine.Sign(ctx, workflow.SignInput{
				TaskID: taskID, SignerUserID: makerID, RequestID: "rid-true-race",
			})
		}()
	}
	close(start)
	wg.Wait()

	var events int
	_ = pool.QueryRow(ctx, `SELECT COUNT(*) FROM signature_events WHERE task_id=$1`, taskID).Scan(&events)
	if events != 1 {
		t.Errorf("RACE VIOLATION: want exactly 1 signature_event, got %d", events)
	}

	for i, e := range errs {
		if e == nil {
			continue
		}
		var saa workflow.ErrStepAlreadyActioned
		if !errors.As(e, &saa) {
			t.Errorf("goroutine %d: unexpected error class (want nil or ErrStepAlreadyActioned): %v", i, e)
		}
	}
}
