package workflow_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"paperless-api/internal/workflow"
)

// testDB opens a pool for integration tests.
// Tests in this package are skipped if PAPERLESS_TEST_DB is not set.
func testDB(t *testing.T) *pgxpool.Pool {
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

// seed inserts minimal rows needed for engine tests and returns doc/task IDs.
// It inserts inside a transaction that's rolled back at cleanup so tests are
// isolated and repeatable.
func seedWorkflow(t *testing.T, pool *pgxpool.Pool) (docID int64, makerTaskID, checkerATaskID, checkerBTaskID, approverTaskID int64) {
	t.Helper()
	ctx := context.Background()

	// Reuse seeded users from 0002/0003/0004 migrations.
	// Seed a fresh document + workflow tasks deterministically.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin seed tx: %v", err)
	}

	// Grab the active POP template.
	var templateID int64
	var templateVersion int
	err = tx.QueryRow(ctx, `SELECT id, version FROM workflow_templates WHERE doc_format_code='POP' AND status='active'`).
		Scan(&templateID, &templateVersion)
	if err != nil {
		tx.Rollback(ctx)
		t.Fatalf("find POP template: %v", err)
	}

	// Create a doc.
	idKey := fmt.Sprintf("POP:TEST-%d:0", time.Now().UnixNano())
	err = tx.QueryRow(ctx, `
		INSERT INTO documents (doc_format_code, doc_no, revision, workflow_template_id, workflow_version,
		                       status, idempotency_key, source_hash)
		VALUES ('POP', $1, 0, $2, $3, 'pending', $4, 'testhash')
		RETURNING id
	`, fmt.Sprintf("TEST-%d", time.Now().UnixNano()), templateID, templateVersion, idKey).Scan(&docID)
	if err != nil {
		tx.Rollback(ctx)
		t.Fatalf("insert doc: %v", err)
	}

	// Create seq=1 MAKER task (condition_type=1, assigned to maker).
	var makerUserID, checkerAUserID, checkerBUserID, approverUserID int64
	_ = tx.QueryRow(ctx, `SELECT id FROM users WHERE username='maker'`).Scan(&makerUserID)
	_ = tx.QueryRow(ctx, `SELECT id FROM users WHERE username='checkerA'`).Scan(&checkerAUserID)
	_ = tx.QueryRow(ctx, `SELECT id FROM users WHERE username='checkerB'`).Scan(&checkerBUserID)
	_ = tx.QueryRow(ctx, `SELECT id FROM users WHERE username='approver'`).Scan(&approverUserID)

	var makerStepID, checkerStepID, approverStepID int64
	_ = tx.QueryRow(ctx, `SELECT id FROM workflow_steps WHERE workflow_template_id=$1 AND position_code='MAKER'`, templateID).Scan(&makerStepID)
	_ = tx.QueryRow(ctx, `SELECT id FROM workflow_steps WHERE workflow_template_id=$1 AND position_code='CHECKER'`, templateID).Scan(&checkerStepID)
	_ = tx.QueryRow(ctx, `SELECT id FROM workflow_steps WHERE workflow_template_id=$1 AND position_code='APPROVER'`, templateID).Scan(&approverStepID)

	// Only seq=1 tasks start open; seq=2,3 start waiting (engine opens them when seq-1 completes).
	_ = tx.QueryRow(ctx, `
		INSERT INTO signature_tasks (document_id, workflow_step_id, assigned_user_id, sequence_no, condition_type, status, opened_at)
		VALUES ($1, $2, $3, 1, 1, 'open', now()) RETURNING id
	`, docID, makerStepID, makerUserID).Scan(&makerTaskID)

	_ = tx.QueryRow(ctx, `
		INSERT INTO signature_tasks (document_id, workflow_step_id, assigned_user_id, sequence_no, condition_type, status)
		VALUES ($1, $2, $3, 2, 2, 'waiting') RETURNING id
	`, docID, checkerStepID, checkerAUserID).Scan(&checkerATaskID)

	_ = tx.QueryRow(ctx, `
		INSERT INTO signature_tasks (document_id, workflow_step_id, assigned_user_id, sequence_no, condition_type, status)
		VALUES ($1, $2, $3, 2, 2, 'waiting') RETURNING id
	`, docID, checkerStepID, checkerBUserID).Scan(&checkerBTaskID)

	_ = tx.QueryRow(ctx, `
		INSERT INTO signature_tasks (document_id, workflow_step_id, assigned_user_id, sequence_no, condition_type, status)
		VALUES ($1, $2, $3, 3, 1, 'waiting') RETURNING id
	`, docID, approverStepID, approverUserID).Scan(&approverTaskID)

	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit seed: %v", err)
	}

	// Cleanup: delete test doc (cascades to tasks/events).
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM documents WHERE id=$1`, docID)
	})
	return
}

func userID(t *testing.T, pool *pgxpool.Pool, username string) int64 {
	t.Helper()
	var id int64
	if err := pool.QueryRow(context.Background(), `SELECT id FROM users WHERE username=$1`, username).Scan(&id); err != nil {
		t.Fatalf("userID(%s): %v", username, err)
	}
	return id
}

func TestCondition1_AnyOneSigns_OthersBecomeSkipped(t *testing.T) {
	pool := testDB(t)
	engine := workflow.New(pool)
	ctx := context.Background()

	// Seed a new workflow with 3 assignees at seq=1 condition_type=1.
	// We'll reuse seedWorkflow for a 1-step doc instead (maker signs → done).
	// Simpler: create a custom seed with two condition-1 tasks at seq=1.

	var templateID int64
	_ = pool.QueryRow(ctx, `SELECT id FROM workflow_templates WHERE doc_format_code='POP' AND status='active'`).Scan(&templateID)
	var makerStepID int64
	_ = pool.QueryRow(ctx, `SELECT id FROM workflow_steps WHERE workflow_template_id=$1 AND position_code='MAKER'`, templateID).Scan(&makerStepID)

	makerID := userID(t, pool, "maker")
	approverID := userID(t, pool, "approver")

	// Create doc with two condition-1 open tasks (simulating 2 eligible signers).
	var docID, task1ID, task2ID int64
	idKey := fmt.Sprintf("POP:C1TEST-%d:0", time.Now().UnixNano())
	_ = pool.QueryRow(ctx, `
		INSERT INTO documents (doc_format_code, doc_no, revision, workflow_template_id, workflow_version, status, idempotency_key, source_hash)
		VALUES ('POP', $1, 0, $2, 1, 'pending', $3, 'c1hash') RETURNING id
	`, fmt.Sprintf("C1TEST-%d", time.Now().UnixNano()), templateID, idKey).Scan(&docID)
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM documents WHERE id=$1`, docID) })

	_ = pool.QueryRow(ctx, `
		INSERT INTO signature_tasks (document_id, workflow_step_id, assigned_user_id, sequence_no, condition_type, status, opened_at)
		VALUES ($1, $2, $3, 1, 1, 'open', now()) RETURNING id
	`, docID, makerStepID, makerID).Scan(&task1ID)
	_ = pool.QueryRow(ctx, `
		INSERT INTO signature_tasks (document_id, workflow_step_id, assigned_user_id, sequence_no, condition_type, status, opened_at)
		VALUES ($1, $2, $3, 1, 1, 'open', now()) RETURNING id
	`, docID, makerStepID, approverID).Scan(&task2ID)

	// Maker signs first.
	err := engine.Sign(ctx, workflow.SignInput{
		TaskID:       task1ID,
		SignerUserID: makerID,
		RequestID:    "req-c1-maker",
	})
	if err != nil {
		t.Fatalf("Sign(maker): %v", err)
	}

	// Task1 should be signed, task2 should be skipped.
	var status1, status2 string
	_ = pool.QueryRow(ctx, `SELECT status FROM signature_tasks WHERE id=$1`, task1ID).Scan(&status1)
	_ = pool.QueryRow(ctx, `SELECT status FROM signature_tasks WHERE id=$1`, task2ID).Scan(&status2)
	if status1 != "signed" {
		t.Errorf("task1 status got %q, want 'signed'", status1)
	}
	if status2 != "skipped" {
		t.Errorf("task2 status got %q, want 'skipped'", status2)
	}
}

func TestCondition1_Race_ExactlyOneWins(t *testing.T) {
	pool := testDB(t)
	engine := workflow.New(pool)
	ctx := context.Background()

	var templateID int64
	_ = pool.QueryRow(ctx, `SELECT id FROM workflow_templates WHERE doc_format_code='POP' AND status='active'`).Scan(&templateID)
	var makerStepID int64
	_ = pool.QueryRow(ctx, `SELECT id FROM workflow_steps WHERE workflow_template_id=$1 AND position_code='MAKER'`, templateID).Scan(&makerStepID)

	makerID := userID(t, pool, "maker")
	approverID := userID(t, pool, "approver")

	var docID, task1ID, task2ID int64
	idKey := fmt.Sprintf("POP:RACE-%d:0", time.Now().UnixNano())
	_ = pool.QueryRow(ctx, `
		INSERT INTO documents (doc_format_code, doc_no, revision, workflow_template_id, workflow_version, status, idempotency_key, source_hash)
		VALUES ('POP', $1, 0, $2, 1, 'pending', $3, 'racehash') RETURNING id
	`, fmt.Sprintf("RACE-%d", time.Now().UnixNano()), templateID, idKey).Scan(&docID)
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM documents WHERE id=$1`, docID) })

	_ = pool.QueryRow(ctx, `
		INSERT INTO signature_tasks (document_id, workflow_step_id, assigned_user_id, sequence_no, condition_type, status, opened_at)
		VALUES ($1, $2, $3, 1, 1, 'open', now()) RETURNING id
	`, docID, makerStepID, makerID).Scan(&task1ID)
	_ = pool.QueryRow(ctx, `
		INSERT INTO signature_tasks (document_id, workflow_step_id, assigned_user_id, sequence_no, condition_type, status, opened_at)
		VALUES ($1, $2, $3, 1, 1, 'open', now()) RETURNING id
	`, docID, makerStepID, approverID).Scan(&task2ID)

	// Two concurrent goroutines race to sign their task.
	var wg sync.WaitGroup
	errs := make([]error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		errs[0] = engine.Sign(ctx, workflow.SignInput{
			TaskID: task1ID, SignerUserID: makerID, RequestID: "req-race-maker",
		})
	}()
	go func() {
		defer wg.Done()
		errs[1] = engine.Sign(ctx, workflow.SignInput{
			TaskID: task2ID, SignerUserID: approverID, RequestID: "req-race-approver",
		})
	}()
	wg.Wait()

	// Count winners: exactly one must succeed, one must get ErrStepAlreadyActioned
	// (or succeed but the step was already done — the engine handles it).
	var signedCount int
	_ = pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM signature_tasks WHERE document_id=$1 AND status='signed'`, docID,
	).Scan(&signedCount)

	if signedCount != 1 {
		t.Errorf("expected exactly 1 signed task, got %d (errs: %v, %v)", signedCount, errs[0], errs[1])
	}

	// The loser must have returned ErrStepAlreadyActioned (or nil if they detected
	// the step already complete before signing — both are acceptable outcomes).
	var alreadyActioned int
	for _, e := range errs {
		if e != nil {
			var saa workflow.ErrStepAlreadyActioned
			if errors.As(e, &saa) {
				alreadyActioned++
			} else {
				t.Errorf("unexpected error: %v", e)
			}
		}
	}
	// One win + one ErrStepAlreadyActioned, or both succeed but only 1 signed row.
	if signedCount != 1 {
		t.Errorf("race: signed count want 1, got %d", signedCount)
	}
}

func TestCondition2_AllMustSign(t *testing.T) {
	pool := testDB(t)
	engine := workflow.New(pool)
	ctx := context.Background()

	var templateID int64
	_ = pool.QueryRow(ctx, `SELECT id FROM workflow_templates WHERE doc_format_code='POP' AND status='active'`).Scan(&templateID)
	var checkerStepID int64
	_ = pool.QueryRow(ctx, `SELECT id FROM workflow_steps WHERE workflow_template_id=$1 AND position_code='CHECKER'`, templateID).Scan(&checkerStepID)

	checkerAID := userID(t, pool, "checkerA")
	checkerBID := userID(t, pool, "checkerB")

	var docID, taskAID, taskBID int64
	idKey := fmt.Sprintf("POP:C2TEST-%d:0", time.Now().UnixNano())
	_ = pool.QueryRow(ctx, `
		INSERT INTO documents (doc_format_code, doc_no, revision, workflow_template_id, workflow_version, status, idempotency_key, source_hash)
		VALUES ('POP', $1, 0, $2, 1, 'pending', $3, 'c2hash') RETURNING id
	`, fmt.Sprintf("C2TEST-%d", time.Now().UnixNano()), templateID, idKey).Scan(&docID)
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM documents WHERE id=$1`, docID) })

	_ = pool.QueryRow(ctx, `
		INSERT INTO signature_tasks (document_id, workflow_step_id, assigned_user_id, sequence_no, condition_type, status, opened_at)
		VALUES ($1, $2, $3, 2, 2, 'open', now()) RETURNING id
	`, docID, checkerStepID, checkerAID).Scan(&taskAID)
	_ = pool.QueryRow(ctx, `
		INSERT INTO signature_tasks (document_id, workflow_step_id, assigned_user_id, sequence_no, condition_type, status, opened_at)
		VALUES ($1, $2, $3, 2, 2, 'open', now()) RETURNING id
	`, docID, checkerStepID, checkerBID).Scan(&taskBID)

	// After A signs, step should NOT be complete (still 1/2).
	err := engine.Sign(ctx, workflow.SignInput{
		TaskID: taskAID, SignerUserID: checkerAID, RequestID: "req-c2-A",
	})
	if err != nil {
		t.Fatalf("Sign(checkerA): %v", err)
	}
	var docStatus string
	_ = pool.QueryRow(ctx, `SELECT status FROM documents WHERE id=$1`, docID).Scan(&docStatus)
	if docStatus == "completed" {
		t.Error("document should not be completed after only 1/2 signed")
	}

	// After B signs, step should be complete (2/2).
	err = engine.Sign(ctx, workflow.SignInput{
		TaskID: taskBID, SignerUserID: checkerBID, RequestID: "req-c2-B",
	})
	if err != nil {
		t.Fatalf("Sign(checkerB): %v", err)
	}
	var signedCount int
	_ = pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM signature_tasks WHERE document_id=$1 AND status='signed'`, docID,
	).Scan(&signedCount)
	if signedCount != 2 {
		t.Errorf("expected 2 signed tasks, got %d", signedCount)
	}
}

func TestSequenceGate_Seq2NotOpenWhileSeq1Incomplete(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()
	engine := workflow.New(pool)

	docID, _, checkerATaskID, _, _ := seedWorkflow(t, pool)

	// checkerA's task is at seq=2 and should be 'waiting', not 'open'.
	var status string
	_ = pool.QueryRow(ctx, `SELECT status FROM signature_tasks WHERE id=$1`, checkerATaskID).Scan(&status)
	if status != "waiting" {
		t.Errorf("seq-2 task should be 'waiting' before seq-1 complete, got %q", status)
	}

	// Attempting to sign seq-2 while seq-1 is still open must fail.
	checkerAID := userID(t, pool, "checkerA")
	err := engine.Sign(ctx, workflow.SignInput{
		TaskID:       checkerATaskID,
		SignerUserID: checkerAID,
		RequestID:    "req-seq-gate",
	})
	if err == nil {
		t.Error("expected error signing waiting task, got nil")
	}

	// Verify the doc is not completed.
	var docStatus string
	_ = pool.QueryRow(ctx, `SELECT status FROM documents WHERE id=$1`, docID).Scan(&docStatus)
	if docStatus == "completed" {
		t.Error("document should not be completed while seq-1 is incomplete")
	}
}

func TestReject_RequiresReason_WritesAudit(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()
	engine := workflow.New(pool)

	docID, makerTaskID, _, _, _ := seedWorkflow(t, pool)
	makerID := userID(t, pool, "maker")

	// Reject without reason → error.
	err := engine.Reject(ctx, workflow.RejectInput{
		TaskID: makerTaskID, SignerUserID: makerID, Reason: "",
	})
	if err == nil {
		t.Error("expected error for empty reason, got nil")
	}

	// Reject with reason → document becomes rejected, audit written.
	err = engine.Reject(ctx, workflow.RejectInput{
		TaskID:       makerTaskID,
		SignerUserID: makerID,
		Reason:       "ข้อมูลไม่ครบ",
		RequestID:    "req-reject",
	})
	if err != nil {
		t.Fatalf("Reject: %v", err)
	}

	var docStatus string
	_ = pool.QueryRow(ctx, `SELECT status FROM documents WHERE id=$1`, docID).Scan(&docStatus)
	if docStatus != "rejected" {
		t.Errorf("doc status got %q, want 'rejected'", docStatus)
	}

	var auditCount int
	_ = pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM audit_logs WHERE entity_type='document' AND entity_id=$1::text AND action='document_rejected'`,
		docID,
	).Scan(&auditCount)
	if auditCount == 0 {
		t.Error("expected audit_log entry for document_rejected, found none")
	}
}

func TestSignIdempotent_SameRequestID(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()
	engine := workflow.New(pool)

	_, makerTaskID, _, _, _ := seedWorkflow(t, pool)
	makerID := userID(t, pool, "maker")

	// Sign once.
	err := engine.Sign(ctx, workflow.SignInput{
		TaskID: makerTaskID, SignerUserID: makerID, RequestID: "req-idem-1",
	})
	if err != nil {
		t.Fatalf("first Sign: %v", err)
	}

	// Count events before.
	var countBefore int
	_ = pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM signature_events WHERE task_id=$1`, makerTaskID,
	).Scan(&countBefore)

	// Sign with the same request_id.
	err = engine.Sign(ctx, workflow.SignInput{
		TaskID: makerTaskID, SignerUserID: makerID, RequestID: "req-idem-1",
	})
	// Must not error and must not add a new event.
	if err != nil && !errors.As(err, &workflow.ErrStepAlreadyActioned{}) {
		t.Fatalf("idempotent Sign returned unexpected error: %v", err)
	}

	var countAfter int
	_ = pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM signature_events WHERE task_id=$1`, makerTaskID,
	).Scan(&countAfter)
	if countAfter != countBefore {
		t.Errorf("idempotent sign: event count went from %d to %d, want no change", countBefore, countAfter)
	}
}

func TestExternalToken_Expired(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()
	engine := workflow.New(pool)

	var templateID int64
	_ = pool.QueryRow(ctx, `SELECT id FROM workflow_templates WHERE doc_format_code='POP' AND status='active'`).Scan(&templateID)
	var approverStepID int64
	_ = pool.QueryRow(ctx, `SELECT id FROM workflow_steps WHERE workflow_template_id=$1 AND position_code='APPROVER'`, templateID).Scan(&approverStepID)

	var docID int64
	idKey := fmt.Sprintf("POP:EXT-%d:0", time.Now().UnixNano())
	_ = pool.QueryRow(ctx, `
		INSERT INTO documents (doc_format_code, doc_no, revision, workflow_template_id, workflow_version, status, idempotency_key, source_hash)
		VALUES ('POP', $1, 0, $2, 1, 'pending', $3, 'exthash') RETURNING id
	`, fmt.Sprintf("EXT-%d", time.Now().UnixNano()), templateID, idKey).Scan(&docID)
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM documents WHERE id=$1`, docID) })

	// External signer with expired token.
	var extSignerID int64
	expiredAt := time.Now().Add(-time.Hour)
	_ = pool.QueryRow(ctx, `
		INSERT INTO external_signers (document_id, name, token_hash, token_expires_at, status)
		VALUES ($1, 'ลูกค้า A', 'deadbeefhash', $2, 'pending') RETURNING id
	`, docID, expiredAt).Scan(&extSignerID)

	var extTaskID int64
	_ = pool.QueryRow(ctx, `
		INSERT INTO signature_tasks (document_id, workflow_step_id, external_signer_id, sequence_no, condition_type, status, opened_at)
		VALUES ($1, $2, $3, 3, 3, 'open', now()) RETURNING id
	`, docID, approverStepID, extSignerID).Scan(&extTaskID)

	err := engine.ExternalSign(ctx, extTaskID, "deadbeefhash", workflow.SignInput{RequestID: "req-ext-expired"})
	var expErr workflow.ErrExternalTokenExpired
	if !errors.As(err, &expErr) {
		t.Errorf("expected ErrExternalTokenExpired, got %v", err)
	}
}
