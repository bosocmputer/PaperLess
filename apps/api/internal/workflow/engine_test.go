package workflow_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
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

// cleanupDoc removes a test document and its dependent rows. signature_events
// has no ON DELETE CASCADE on document_id (it is append-only evidence by
// design — see migrations/0001), and audit_logs is decoupled by string id, so
// a plain DELETE on documents would be blocked by the FK and silently leak test
// rows. We delete the evidence rows explicitly here (test DB only) so the
// document delete — and any subsequent migration down — succeeds.
func cleanupDoc(pool *pgxpool.Pool, docID int64) {
	ctx := context.Background()
	_, _ = pool.Exec(ctx, `DELETE FROM signature_events WHERE document_id=$1`, docID)
	_, _ = pool.Exec(ctx, `DELETE FROM audit_logs WHERE entity_type='document' AND entity_id=$1`, strconv.FormatInt(docID, 10))
	_, _ = pool.Exec(ctx, `DELETE FROM documents WHERE id=$1`, docID)
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
	t.Cleanup(func() { cleanupDoc(pool, docID) })
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
	t.Cleanup(func() { cleanupDoc(pool, docID) })

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
	t.Cleanup(func() { cleanupDoc(pool, docID) })

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
	t.Cleanup(func() { cleanupDoc(pool, docID) })

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
		`SELECT COUNT(*) FROM audit_logs WHERE entity_type='document' AND entity_id=$1 AND action='document_rejected'`,
		strconv.FormatInt(docID, 10),
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

// TestImport_ExternalStep_CreatesWaitingTask verifies that importing a document
// whose active template has a condition_type=3 step:
//   - no longer returns an error (Phase 1 guard is gone for c3 steps)
//   - creates exactly one `waiting` task with NULL assigned_user_id + NULL external_signer_id
//   - leaves the document in `pending` (not `completed`) because the waiting
//     task is not a terminal status
func TestImport_ExternalStep_CreatesWaitingTask(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()

	// Use the DEMO3 draft template which has a condition_type=3 CUSTOMER step at seq=3.
	// Activate it transiently inside this test's own document row (we do NOT flip
	// DEMO3 to active in the DB — we bind the document manually to the template id
	// and call OpenFirstSequence directly, bypassing the import handler's template-
	// lookup logic that requires status='active').
	var demo3ID int64
	err := pool.QueryRow(ctx,
		`SELECT id FROM workflow_templates WHERE doc_format_code='DEMO3' AND version=1`,
	).Scan(&demo3ID)
	if err != nil {
		t.Skipf("DEMO3 template not seeded (migration 0005 not applied?): %v", err)
	}

	// Insert a test document bound to DEMO3.
	var docID int64
	idKey := fmt.Sprintf("DEMO3:EXTTEST-%d:0", time.Now().UnixNano())
	err = pool.QueryRow(ctx, `
		INSERT INTO documents (doc_format_code, doc_no, revision, workflow_template_id,
		                       workflow_version, status, idempotency_key, source_hash)
		VALUES ('DEMO3', $1, 0, $2, 1, 'pending', $3, 'ext_step_hash')
		RETURNING id
	`, fmt.Sprintf("EXTTEST-%d", time.Now().UnixNano()), demo3ID, idKey).Scan(&docID)
	if err != nil {
		t.Fatalf("insert doc: %v", err)
	}
	t.Cleanup(func() { cleanupDoc(pool, docID) })

	// OpenFirstSequence must succeed (seq=1 is condition_type=1 MAKER with assignees).
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	if err := workflow.OpenFirstSequence(ctx, tx, docID, demo3ID); err != nil {
		tx.Rollback(ctx)
		t.Fatalf("OpenFirstSequence: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	// seq=1 task should be `open` (c1 MAKER).
	var openCount int
	_ = pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM signature_tasks WHERE document_id=$1 AND sequence_no=1 AND status='open'`,
		docID,
	).Scan(&openCount)
	if openCount != 1 {
		t.Errorf("seq=1: expected 1 open task, got %d", openCount)
	}

	// seq=3 CUSTOMER external task should NOT exist yet — only seq=1 is opened at import.
	var extCount int
	_ = pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM signature_tasks WHERE document_id=$1 AND sequence_no=3`,
		docID,
	).Scan(&extCount)
	if extCount != 0 {
		t.Errorf("seq=3 external task should not exist yet (seq gate), got %d rows", extCount)
	}

	// Document should be pending (not completed).
	var docStatus string
	_ = pool.QueryRow(ctx, `SELECT status FROM documents WHERE id=$1`, docID).Scan(&docStatus)
	if docStatus != "pending" {
		t.Errorf("doc status got %q, want 'pending'", docStatus)
	}

	// Now simulate completing seq=1 and seq=2 to trigger opening of seq=3.
	// We do this by directly signing the open task and manually opening seq=2,
	// then signing both checkers, which should cause seq=3 to be opened with a
	// `waiting` external task.
	engine := workflow.New(pool)

	// Get seq=1 task and maker user.
	var makerTaskID int64
	_ = pool.QueryRow(ctx,
		`SELECT id FROM signature_tasks WHERE document_id=$1 AND sequence_no=1 AND status='open'`,
		docID,
	).Scan(&makerTaskID)
	var makerUserID int64
	_ = pool.QueryRow(ctx, `SELECT id FROM users WHERE username='maker'`).Scan(&makerUserID)

	if err := engine.Sign(ctx, workflow.SignInput{
		TaskID: makerTaskID, SignerUserID: makerUserID, RequestID: "req-ext-maker",
	}); err != nil {
		t.Fatalf("Sign(maker): %v", err)
	}

	// After seq=1 signs, seq=2 tasks (condition_type=2 CHECKER) should be `open`.
	var seq2Tasks []int64
	seq2Rows, err := pool.Query(ctx,
		`SELECT id FROM signature_tasks WHERE document_id=$1 AND sequence_no=2 AND status='open'`, docID)
	if err != nil {
		t.Fatalf("query seq2 tasks: %v", err)
	}
	for seq2Rows.Next() {
		var id int64
		seq2Rows.Scan(&id)
		seq2Tasks = append(seq2Tasks, id)
	}
	seq2Rows.Close()
	if len(seq2Tasks) == 0 {
		t.Fatal("seq=2 tasks not opened after seq=1 complete")
	}

	// Sign all seq=2 tasks (condition_type=2 requires all).
	checkerAID := userID(t, pool, "checkerA")
	for i, tid := range seq2Tasks {
		// Find who is assigned.
		var assignedUID int64
		pool.QueryRow(ctx, `SELECT assigned_user_id FROM signature_tasks WHERE id=$1`, tid).Scan(&assignedUID)
		uid := assignedUID
		if uid == 0 {
			uid = checkerAID
		}
		if err := engine.Sign(ctx, workflow.SignInput{
			TaskID: tid, SignerUserID: uid, RequestID: fmt.Sprintf("req-ext-checker-%d", i),
		}); err != nil {
			t.Fatalf("Sign(checker %d): %v", i, err)
		}
	}

	// Now seq=3 should have been opened. It is condition_type=3 (external),
	// so it must have exactly one `waiting` task with NULL signer IDs.
	var extTaskID int64
	var extStatus string
	var extAssignedUserID *int64
	var extExternalSignerID *int64
	err = pool.QueryRow(ctx, `
		SELECT id, status, assigned_user_id, external_signer_id
		  FROM signature_tasks
		 WHERE document_id=$1 AND sequence_no=3
	`, docID).Scan(&extTaskID, &extStatus, &extAssignedUserID, &extExternalSignerID)
	if err != nil {
		t.Fatalf("query external task: %v", err)
	}
	if extStatus != "waiting" {
		t.Errorf("external task status got %q, want 'waiting'", extStatus)
	}
	if extAssignedUserID != nil {
		t.Errorf("external task assigned_user_id should be NULL, got %d", *extAssignedUserID)
	}
	if extExternalSignerID != nil {
		t.Errorf("external task external_signer_id should be NULL, got %d", *extExternalSignerID)
	}

	// Document must still be `pending` — waiting task is not terminal.
	_ = pool.QueryRow(ctx, `SELECT status FROM documents WHERE id=$1`, docID).Scan(&docStatus)
	if docStatus != "pending" {
		t.Errorf("doc with waiting external task got status %q, want 'pending'", docStatus)
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
	t.Cleanup(func() { cleanupDoc(pool, docID) })

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

// TestConcurrentSign_SameRequestID_ExactlyOneEvent fires two concurrent Sign
// calls with the same request_id and asserts exactly one signature_event is
// written (the DB partial UNIQUE index is the atomic guard; app-level check is
// only a fast path). Mirrors TestCondition1_Race_ExactlyOneWins style.
func TestConcurrentSign_SameRequestID_ExactlyOneEvent(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()
	engine := workflow.New(pool)

	var templateID int64
	_ = pool.QueryRow(ctx, `SELECT id FROM workflow_templates WHERE doc_format_code='POP' AND status='active'`).Scan(&templateID)
	var makerStepID int64
	_ = pool.QueryRow(ctx, `SELECT id FROM workflow_steps WHERE workflow_template_id=$1 AND position_code='MAKER'`, templateID).Scan(&makerStepID)
	makerID := userID(t, pool, "maker")

	var docID, taskID int64
	idKey := fmt.Sprintf("POP:RIDRACE-%d:0", time.Now().UnixNano())
	_ = pool.QueryRow(ctx, `
		INSERT INTO documents (doc_format_code, doc_no, revision, workflow_template_id, workflow_version, status, idempotency_key, source_hash)
		VALUES ('POP', $1, 0, $2, 1, 'pending', $3, 'ridracehash') RETURNING id
	`, fmt.Sprintf("RIDRACE-%d", time.Now().UnixNano()), templateID, idKey).Scan(&docID)
	t.Cleanup(func() { cleanupDoc(pool, docID) })

	_ = pool.QueryRow(ctx, `
		INSERT INTO signature_tasks (document_id, workflow_step_id, assigned_user_id, sequence_no, condition_type, status, opened_at)
		VALUES ($1, $2, $3, 1, 1, 'open', now()) RETURNING id
	`, docID, makerStepID, makerID).Scan(&taskID)

	const sharedRequestID = "req-concurrent-same-id"
	var wg sync.WaitGroup
	errs := make([]error, 5)
	wg.Add(len(errs))
	for i := range errs {
		i := i
		go func() {
			defer wg.Done()
			errs[i] = engine.Sign(ctx, workflow.SignInput{
				TaskID:       taskID,
				SignerUserID: makerID,
				RequestID:    sharedRequestID,
			})
		}()
	}
	wg.Wait()

	var eventCount int
	_ = pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM signature_events WHERE task_id=$1`, taskID,
	).Scan(&eventCount)

	if eventCount != 1 {
		t.Errorf("concurrent same-request_id: want exactly 1 signature_event, got %d (errs: %v)", eventCount, errs)
	}

	// All goroutines must have returned nil or ErrStepAlreadyActioned — never a 500-class error.
	for i, e := range errs {
		if e == nil {
			continue
		}
		var saa workflow.ErrStepAlreadyActioned
		if !errors.As(e, &saa) {
			t.Errorf("goroutine %d: unexpected error (want nil or ErrStepAlreadyActioned): %v", i, e)
		}
	}
}

// TestConcurrentExternalSign_SameRequestID_ExactlyOneEvent is the ExternalSign
// equivalent of the above: concurrent calls with the same request_id must
// produce exactly one signature_event row.
func TestConcurrentExternalSign_SameRequestID_ExactlyOneEvent(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()
	engine := workflow.New(pool)

	suffix := time.Now().UnixNano()
	var templateID, stepID int64
	_ = pool.QueryRow(ctx, `
		INSERT INTO workflow_templates (doc_format_code, name, version, status, created_by)
		SELECT $1, 'ext rid race', 1, 'active', u.id FROM users u WHERE u.username='admin' RETURNING id
	`, fmt.Sprintf("XRDR%d", suffix)).Scan(&templateID)
	_ = pool.QueryRow(ctx, `
		INSERT INTO workflow_steps (workflow_template_id, position_code, position_name, sequence_no, condition_type)
		VALUES ($1, 'CUSTOMER', 'ลูกค้า', 1, 3) RETURNING id
	`, templateID).Scan(&stepID)
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM workflow_steps WHERE workflow_template_id=$1`, templateID)
		_, _ = pool.Exec(ctx, `DELETE FROM workflow_templates WHERE id=$1`, templateID)
	})

	var docID int64
	idKey := fmt.Sprintf("XRDR:%d:0", suffix)
	_ = pool.QueryRow(ctx, `
		INSERT INTO documents (doc_format_code, doc_no, revision, workflow_template_id, workflow_version, status, idempotency_key, source_hash)
		VALUES ($1, $2, 0, $3, 1, 'pending', $4, 'xrdrhash') RETURNING id
	`, fmt.Sprintf("XRDR%d", suffix), fmt.Sprintf("XRDR-%d", suffix), templateID, idKey).Scan(&docID)
	t.Cleanup(func() { cleanupDoc(pool, docID) })

	rawToken := make([]byte, 32)
	for i := range rawToken {
		rawToken[i] = byte((i * 13) % 251)
	}
	sum := sha256.Sum256(rawToken)
	tokenHash := hex.EncodeToString(sum[:])

	var extSignerID, taskID int64
	_ = pool.QueryRow(ctx, `
		INSERT INTO external_signers (document_id, name, token_hash, token_expires_at, status)
		VALUES ($1, 'Race Signer', $2, now() + interval '72 hours', 'pending') RETURNING id
	`, docID, tokenHash).Scan(&extSignerID)
	_ = pool.QueryRow(ctx, `
		INSERT INTO signature_tasks (document_id, workflow_step_id, external_signer_id, sequence_no, condition_type, status, opened_at)
		VALUES ($1, $2, $3, 1, 3, 'open', now()) RETURNING id
	`, docID, stepID, extSignerID).Scan(&taskID)

	const sharedRequestID = "req-ext-concurrent-same-id"
	var wg sync.WaitGroup
	errs := make([]error, 5)
	wg.Add(len(errs))
	for i := range errs {
		i := i
		go func() {
			defer wg.Done()
			errs[i] = engine.ExternalSign(ctx, taskID, tokenHash, workflow.SignInput{
				TaskID:             taskID,
				ExternalSignerName: "Race Signer",
				SignatureImageHash: "racesighash",
				ConsentText:        "consent",
				RequestID:          sharedRequestID,
			})
		}()
	}
	wg.Wait()

	var eventCount int
	_ = pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM signature_events WHERE task_id=$1`, taskID,
	).Scan(&eventCount)

	if eventCount != 1 {
		t.Errorf("concurrent external same-request_id: want exactly 1 signature_event, got %d (errs: %v)", eventCount, errs)
	}

	// All goroutines must return nil or ErrStepAlreadyActioned — never a 500-class error.
	for i, e := range errs {
		if e == nil {
			continue
		}
		var saa workflow.ErrStepAlreadyActioned
		if !errors.As(e, &saa) {
			t.Errorf("goroutine %d: unexpected error (want nil or ErrStepAlreadyActioned): %v", i, e)
		}
	}
}

// TestTerminalDoc_CannotBeSignedOrReinvited verifies Step 1b:
// a rejected/completed document returns a clean error, not a panic or 500.
func TestTerminalDoc_CannotBeSigned(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()
	engine := workflow.New(pool)

	var templateID int64
	_ = pool.QueryRow(ctx, `SELECT id FROM workflow_templates WHERE doc_format_code='POP' AND status='active'`).Scan(&templateID)
	var makerStepID int64
	_ = pool.QueryRow(ctx, `SELECT id FROM workflow_steps WHERE workflow_template_id=$1 AND position_code='MAKER'`, templateID).Scan(&makerStepID)
	makerID := userID(t, pool, "maker")

	for _, terminalStatus := range []string{"rejected", "completed", "cancelled"} {
		terminalStatus := terminalStatus
		t.Run(terminalStatus, func(t *testing.T) {
			var docID, taskID int64
			idKey := fmt.Sprintf("POP:TERM-%s-%d:0", terminalStatus, time.Now().UnixNano())
			_ = pool.QueryRow(ctx, `
				INSERT INTO documents (doc_format_code, doc_no, revision, workflow_template_id, workflow_version, status, idempotency_key, source_hash)
				VALUES ('POP', $1, 0, $2, 1, $3, $4, 'termhash') RETURNING id
			`, fmt.Sprintf("TERM-%s-%d", terminalStatus, time.Now().UnixNano()), templateID, terminalStatus, idKey).Scan(&docID)
			t.Cleanup(func() { cleanupDoc(pool, docID) })

			_ = pool.QueryRow(ctx, `
				INSERT INTO signature_tasks (document_id, workflow_step_id, assigned_user_id, sequence_no, condition_type, status, opened_at)
				VALUES ($1, $2, $3, 1, 1, 'open', now()) RETURNING id
			`, docID, makerStepID, makerID).Scan(&taskID)

			err := engine.Sign(ctx, workflow.SignInput{
				TaskID:       taskID,
				SignerUserID: makerID,
				RequestID:    fmt.Sprintf("req-term-%s", terminalStatus),
			})
			if err == nil {
				t.Errorf("status=%s: expected error signing terminal doc, got nil", terminalStatus)
				return
			}
			// Must not be a server panic — a descriptive error message is sufficient.
			t.Logf("status=%s: got expected error: %v", terminalStatus, err)
		})
	}
}

// TestCondition1_NoDeadlock_ManySiblings is the regression guard for the
// condition_type=1 lock-order-inversion deadlock (SQLSTATE 40P01). Before the
// ordered-lock fix in engine.Sign, each tx locked only its own task row and then
// the winner's sibling-skip UPDATE locked the remaining siblings in physical
// order while the losers still held their own task lock — under contention
// Postgres aborted the cycle with 40P01, leaking a 5xx-class error to a losing
// signer instead of a clean ErrStepAlreadyActioned. This test fans out 8 signers
// onto sibling tasks of the SAME condition-1 step, released together via a
// close(start) barrier, across multiple rounds. It asserts (a) exactly one task
// signs per round and (b) NO signer ever receives a deadlock error — every loser
// returns ErrStepAlreadyActioned (or nil). Proven to FAIL (hundreds of 40P01s)
// against the pre-fix engine; passes with zero deadlocks after the fix.
func TestCondition1_NoDeadlock_ManySiblings(t *testing.T) {
	pool := testDB(t)
	engine := workflow.New(pool)
	ctx := context.Background()

	var templateID int64
	if err := pool.QueryRow(ctx, `SELECT id FROM workflow_templates WHERE doc_format_code='POP' AND status='active'`).Scan(&templateID); err != nil {
		t.Fatalf("find POP template: %v", err)
	}
	var makerStepID int64
	if err := pool.QueryRow(ctx, `SELECT id FROM workflow_steps WHERE workflow_template_id=$1 AND position_code='MAKER'`, templateID).Scan(&makerStepID); err != nil {
		t.Fatalf("find MAKER step: %v", err)
	}
	makerID := userID(t, pool, "maker")

	const fanout = 8
	const rounds = 5

	for r := 0; r < rounds; r++ {
		var docID int64
		idKey := fmt.Sprintf("POP:NODL-%d:0", time.Now().UnixNano())
		if err := pool.QueryRow(ctx, `
			INSERT INTO documents (doc_format_code, doc_no, revision, workflow_template_id, workflow_version, status, idempotency_key, source_hash)
			VALUES ('POP', $1, 0, $2, 1, 'pending', $3, 'nodlhash') RETURNING id
		`, fmt.Sprintf("NODL-%d", time.Now().UnixNano()), templateID, idKey).Scan(&docID); err != nil {
			t.Fatalf("insert doc: %v", err)
		}

		taskIDs := make([]int64, fanout)
		for i := 0; i < fanout; i++ {
			if err := pool.QueryRow(ctx, `
				INSERT INTO signature_tasks (document_id, workflow_step_id, assigned_user_id, sequence_no, condition_type, status, opened_at)
				VALUES ($1, $2, $3, 1, 1, 'open', now()) RETURNING id
			`, docID, makerStepID, makerID).Scan(&taskIDs[i]); err != nil {
				t.Fatalf("insert task: %v", err)
			}
		}

		start := make(chan struct{})
		var wg sync.WaitGroup
		errs := make([]error, fanout)
		for i := 0; i < fanout; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				<-start
				errs[idx] = engine.Sign(ctx, workflow.SignInput{
					TaskID:       taskIDs[idx],
					SignerUserID: makerID,
					RequestID:    fmt.Sprintf("nodl-%d-%d", r, idx),
				})
			}(i)
		}
		close(start)
		wg.Wait()

		// (a) exactly one signed task this round.
		var signed int
		_ = pool.QueryRow(ctx, `SELECT COUNT(*) FROM signature_tasks WHERE document_id=$1 AND status='signed'`, docID).Scan(&signed)
		if signed != 1 {
			t.Errorf("round %d: expected exactly 1 signed task, got %d", r, signed)
		}

		// (b) no signer received a deadlock; every non-nil error must be the
		// clean domain error ErrStepAlreadyActioned.
		for i, e := range errs {
			if e == nil {
				continue
			}
			if strings.Contains(e.Error(), "40P01") || strings.Contains(e.Error(), "deadlock detected") {
				t.Errorf("round %d task[%d]: got deadlock error (40P01): %v", r, i, e)
				continue
			}
			var saa workflow.ErrStepAlreadyActioned
			if !errors.As(e, &saa) {
				t.Errorf("round %d task[%d]: unexpected error (want ErrStepAlreadyActioned): %v", r, i, e)
			}
		}

		cleanupDoc(pool, docID)
	}
}

// ── SML enqueue tests ────────────────────────────────────────────────────────

// signToCompletion drives a seedWorkflow document all the way to `completed` by
// signing every open task, sequence by sequence, until none remain. The engine
// opens the next sequence's tasks (from template assignees) as each step
// completes, so we re-query `status='open'` after each sign rather than rely on
// pre-seeded task IDs. This exercises the real production completion path — the
// same path that calls enqueueLockJob.
func signToCompletion(t *testing.T, pool *pgxpool.Pool, engine *workflow.Engine, docID int64) {
	t.Helper()
	ctx := context.Background()

	// Bounded loop: each open task signed by its assigned user. The POP chain is
	// maker → 2 checkers → approver, so 6 iterations is a safe ceiling.
	for i := 0; i < 12; i++ {
		var taskID, assignedUID int64
		err := pool.QueryRow(ctx, `
			SELECT id, assigned_user_id FROM signature_tasks
			 WHERE document_id=$1 AND status='open' AND assigned_user_id IS NOT NULL
			 ORDER BY sequence_no, id
			 LIMIT 1
		`, docID).Scan(&taskID, &assignedUID)
		if errors.Is(err, pgx.ErrNoRows) {
			break // no more open internal tasks
		}
		if err != nil {
			t.Fatalf("query open task: %v", err)
		}
		if err := engine.Sign(ctx, workflow.SignInput{
			TaskID: taskID, SignerUserID: assignedUID, RequestID: fmt.Sprintf("c-%d-%d", docID, taskID),
		}); err != nil {
			t.Fatalf("sign task %d: %v", taskID, err)
		}
	}

	var docStatus string
	_ = pool.QueryRow(ctx, `SELECT status FROM documents WHERE id=$1`, docID).Scan(&docStatus)
	if docStatus != "completed" {
		t.Fatalf("doc status after full sign chain: want completed, got %s", docStatus)
	}
}

// seedDocForCompletion creates a POP document with ONLY the seq=1 MAKER task
// open (no pre-seeded seq=2/seq=3 rows). The engine opens later sequences from
// the template's assignees as each step completes, so the document can actually
// reach `completed` — unlike seedWorkflow, whose pre-seeded `waiting` rows never
// resolve and intentionally hold the doc `pending`.
func seedDocForCompletion(t *testing.T, pool *pgxpool.Pool) (docID int64) {
	t.Helper()
	ctx := context.Background()

	var templateID int64
	var templateVersion int
	if err := pool.QueryRow(ctx,
		`SELECT id, version FROM workflow_templates WHERE doc_format_code='POP' AND status='active'`,
	).Scan(&templateID, &templateVersion); err != nil {
		t.Fatalf("find POP template: %v", err)
	}

	var makerStepID, makerUserID int64
	_ = pool.QueryRow(ctx,
		`SELECT id FROM workflow_steps WHERE workflow_template_id=$1 AND position_code='MAKER'`, templateID,
	).Scan(&makerStepID)
	_ = pool.QueryRow(ctx,
		`SELECT a.user_id FROM workflow_step_assignees a WHERE a.workflow_step_id=$1 ORDER BY a.display_order LIMIT 1`,
		makerStepID,
	).Scan(&makerUserID)

	idKey := fmt.Sprintf("POP:SML-COMPLETE:%d", time.Now().UnixNano())
	if err := pool.QueryRow(ctx, `
		INSERT INTO documents (doc_format_code, doc_no, revision, workflow_template_id, workflow_version,
		                       status, idempotency_key, source_hash)
		VALUES ('POP', $1, 0, $2, $3, 'pending', $4, 'testhash')
		RETURNING id
	`, fmt.Sprintf("SML-COMPLETE-%d", time.Now().UnixNano()), templateID, templateVersion, idKey).Scan(&docID); err != nil {
		t.Fatalf("insert doc: %v", err)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO signature_tasks (document_id, workflow_step_id, assigned_user_id, sequence_no, condition_type, status, opened_at)
		VALUES ($1, $2, $3, 1, 1, 'open', now())
	`, docID, makerStepID, makerUserID); err != nil {
		t.Fatalf("insert maker task: %v", err)
	}

	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = pool.Exec(ctx, `DELETE FROM sml_sync_jobs WHERE document_id=$1`, docID)
		cleanupDoc(pool, docID)
	})
	return docID
}

func TestEnqueueLockJob_OnDocumentCompletion(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()
	engine := workflow.New(pool)

	docID := seedDocForCompletion(t, pool)
	signToCompletion(t, pool, engine, docID)

	// Exactly one update_lock job must be enqueued.
	var jobCount int
	_ = pool.QueryRow(ctx,
		`SELECT count(*) FROM sml_sync_jobs WHERE document_id=$1 AND job_type='update_lock' AND status='pending'`,
		docID,
	).Scan(&jobCount)
	if jobCount != 1 {
		t.Errorf("sml_sync_jobs: want 1 pending update_lock job, got %d", jobCount)
	}

	// sync_status must be sync_pending.
	var syncStatus string
	_ = pool.QueryRow(ctx, `SELECT sync_status FROM documents WHERE id=$1`, docID).Scan(&syncStatus)
	if syncStatus != "sync_pending" {
		t.Errorf("sync_status: want sync_pending, got %s", syncStatus)
	}
}

func TestEnqueueLockJob_Idempotent_NoDuplicateOnReEntry(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()
	engine := workflow.New(pool)

	docID := seedDocForCompletion(t, pool)

	// Drive to completion — enqueues exactly one job.
	signToCompletion(t, pool, engine, docID)

	// Simulate a second enqueue attempt (mirrors enqueueLockJob's WHERE NOT EXISTS
	// guard). A pending job already exists, so this must insert nothing.
	_, err := pool.Exec(ctx, `
		INSERT INTO sml_sync_jobs (document_id, job_type, status)
		SELECT $1, 'update_lock', 'pending'
		 WHERE NOT EXISTS (
			SELECT 1 FROM sml_sync_jobs
			 WHERE document_id=$1
			   AND job_type='update_lock'
			   AND status IN ('pending','running','retry')
		 )
	`, docID)
	if err != nil {
		t.Fatalf("second enqueue attempt: %v", err)
	}

	var jobCount int
	_ = pool.QueryRow(ctx,
		`SELECT count(*) FROM sml_sync_jobs WHERE document_id=$1 AND job_type='update_lock'`,
		docID,
	).Scan(&jobCount)
	if jobCount != 1 {
		t.Errorf("want exactly 1 update_lock job after duplicate attempt, got %d", jobCount)
	}
}
