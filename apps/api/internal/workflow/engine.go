package workflow

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Engine executes workflow state transitions against the database.
// All public methods are safe to call concurrently; each acquires a row-level
// lock on the relevant step to prevent race conditions for condition_type=1.
type Engine struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Engine {
	return &Engine{pool: pool}
}

// Sign processes a signer's signature on a task.
//
// Invariants enforced inside a single transaction:
//   - Task must be `open` and belong to the signer.
//   - request_id idempotency: same request_id → return existing event, no duplicate.
//   - condition_type=1 race: SELECT … FOR UPDATE on the step; first writer wins,
//     remaining same-step tasks become `skipped`.
//   - After signing, evaluate step completion, then open next-sequence tasks if
//     the step is now complete, then evaluate document completion.
func (e *Engine) Sign(ctx context.Context, in SignInput) error {
	tx, err := e.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// Idempotency: if this request_id already has a signature_event, skip.
	var existingEventID int64
	err = tx.QueryRow(ctx,
		`SELECT id FROM signature_events WHERE request_id=$1 AND task_id=$2`,
		in.RequestID, in.TaskID,
	).Scan(&existingEventID)
	if err == nil {
		// Already processed — idempotent success.
		return tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("check idempotency: %w", err)
	}

	// Load the task with a lock on the workflow step row.
	var task Task
	var stepID int64
	var docStatus string
	err = tx.QueryRow(ctx, `
		SELECT st.id, st.document_id, st.workflow_step_id, st.assigned_user_id,
		       st.sequence_no, st.condition_type, st.status, st.version
		  FROM signature_tasks st
		  JOIN documents d ON d.id = st.document_id
		 WHERE st.id = $1
		   FOR UPDATE OF st
	`, in.TaskID).Scan(
		&task.ID, &task.DocumentID, &stepID, &task.AssignedUserID,
		&task.SequenceNo, &task.ConditionType, &task.Status, &task.Version,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("task %d not found", in.TaskID)
	}
	if err != nil {
		return fmt.Errorf("load task: %w", err)
	}

	// Load doc status (not locked — we re-check after step evaluation).
	_ = tx.QueryRow(ctx, `SELECT status FROM documents WHERE id=$1`, task.DocumentID).Scan(&docStatus)

	if docStatus != string(DocPending) {
		return fmt.Errorf("document is not pending (status=%s)", docStatus)
	}
	if task.Status == TaskSigned || task.Status == TaskSkipped {
		// condition_type=1: another signer already won.
		return ErrStepAlreadyActioned{TaskID: in.TaskID}
	}
	if task.Status != TaskOpen {
		return fmt.Errorf("task is not open (status=%s)", task.Status)
	}
	if task.AssignedUserID == nil || *task.AssignedUserID != in.SignerUserID {
		return fmt.Errorf("user %d is not assigned to task %d", in.SignerUserID, in.TaskID)
	}

	// Mark this task signed.
	if _, err := tx.Exec(ctx,
		`UPDATE signature_tasks SET status='signed', completed_at=now(), version=version+1 WHERE id=$1`,
		in.TaskID,
	); err != nil {
		return fmt.Errorf("mark task signed: %w", err)
	}

	// Write signature event.
	if err := writeSignatureEvent(ctx, tx, in, task); err != nil {
		return err
	}

	// For condition_type=1: skip all sibling open/waiting tasks in the same step.
	if task.ConditionType == ConditionAnyOne {
		if _, err := tx.Exec(ctx, `
			UPDATE signature_tasks
			   SET status='skipped', completed_at=now(), version=version+1
			 WHERE document_id=$1 AND sequence_no=$2 AND id != $3
			   AND status IN ('open','waiting')
		`, task.DocumentID, task.SequenceNo, in.TaskID); err != nil {
			return fmt.Errorf("skip sibling tasks: %w", err)
		}
	}

	// Evaluate step completion and open next sequence if needed.
	stepComplete, err := isStepComplete(ctx, tx, task.DocumentID, task.SequenceNo, task.ConditionType)
	if err != nil {
		return err
	}

	if stepComplete {
		// Write audit.
		if err := writeAuditLog(ctx, tx, "step_complete", "document", task.DocumentID, in.SignerUserID); err != nil {
			return err
		}
		// Open next sequence.
		if err := openNextSequence(ctx, tx, task.DocumentID, task.SequenceNo); err != nil {
			return err
		}
		// Check if the full document is complete.
		docComplete, err := isDocumentComplete(ctx, tx, task.DocumentID)
		if err != nil {
			return err
		}
		if docComplete {
			if _, err := tx.Exec(ctx,
				`UPDATE documents SET status='completed', updated_at=now() WHERE id=$1`,
				task.DocumentID,
			); err != nil {
				return fmt.Errorf("mark doc completed: %w", err)
			}
			if err := writeAuditLog(ctx, tx, "document_completed", "document", task.DocumentID, in.SignerUserID); err != nil {
				return err
			}
		}
	}

	return tx.Commit(ctx)
}

// Reject rejects a document at the given task.
// The document returns to 'pending' (tasks re-evaluated per workflow config).
// Phase 1: rejection drives document to rejected state; admin resets it.
func (e *Engine) Reject(ctx context.Context, in RejectInput) error {
	if in.Reason == "" {
		return fmt.Errorf("reject reason is required")
	}

	tx, err := e.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var task Task
	var docStatus string
	err = tx.QueryRow(ctx, `
		SELECT st.id, st.document_id, st.workflow_step_id, st.assigned_user_id,
		       st.sequence_no, st.condition_type, st.status, st.version
		  FROM signature_tasks st
		  JOIN documents d ON d.id = st.document_id
		 WHERE st.id = $1
		   FOR UPDATE OF st
	`, in.TaskID).Scan(
		&task.ID, &task.DocumentID, &task.WorkflowStepID, &task.AssignedUserID,
		&task.SequenceNo, &task.ConditionType, &task.Status, &task.Version,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("task %d not found", in.TaskID)
	}
	if err != nil {
		return fmt.Errorf("load task: %w", err)
	}

	_ = tx.QueryRow(ctx, `SELECT status FROM documents WHERE id=$1`, task.DocumentID).Scan(&docStatus)
	if docStatus != string(DocPending) {
		return fmt.Errorf("document is not pending (status=%s)", docStatus)
	}
	if task.Status != TaskOpen {
		return fmt.Errorf("task is not open (status=%s)", task.Status)
	}
	if task.AssignedUserID == nil || *task.AssignedUserID != in.SignerUserID {
		return fmt.Errorf("user %d is not assigned to task %d", in.SignerUserID, in.TaskID)
	}

	// Mark this task rejected.
	if _, err := tx.Exec(ctx,
		`UPDATE signature_tasks SET status='rejected', completed_at=now(), version=version+1 WHERE id=$1`,
		in.TaskID,
	); err != nil {
		return fmt.Errorf("mark task rejected: %w", err)
	}

	// Cancel all sibling open tasks in the document.
	if _, err := tx.Exec(ctx, `
		UPDATE signature_tasks SET status='cancelled', completed_at=now(), version=version+1
		 WHERE document_id=$1 AND id != $2 AND status IN ('open','waiting')
	`, task.DocumentID, in.TaskID); err != nil {
		return fmt.Errorf("cancel sibling tasks: %w", err)
	}

	// Write signature event for rejection.
	if err := writeRejectEvent(ctx, tx, in, task); err != nil {
		return err
	}

	// Mark document rejected.
	if _, err := tx.Exec(ctx,
		`UPDATE documents SET status='rejected', updated_at=now() WHERE id=$1`,
		task.DocumentID,
	); err != nil {
		return fmt.Errorf("mark doc rejected: %w", err)
	}

	if err := writeAuditLog(ctx, tx, "document_rejected", "document", task.DocumentID, in.SignerUserID); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// StepProgressForDocument returns the signing progress per step.
func (e *Engine) StepProgressForDocument(ctx context.Context, docID int64) ([]StepProgress, error) {
	rows, err := e.pool.Query(ctx, `
		SELECT sequence_no, condition_type,
		       COUNT(*) FILTER (WHERE status='signed') AS signed,
		       COUNT(*)                                  AS total
		  FROM signature_tasks
		 WHERE document_id=$1
		 GROUP BY sequence_no, condition_type
		 ORDER BY sequence_no
	`, docID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []StepProgress
	for rows.Next() {
		var sp StepProgress
		var ct int16
		if err := rows.Scan(&sp.SequenceNo, &ct, &sp.SignedCount, &sp.TotalCount); err != nil {
			return nil, err
		}
		sp.ConditionType = ConditionType(ct)
		switch sp.ConditionType {
		case ConditionAnyOne:
			sp.Complete = sp.SignedCount >= 1
		case ConditionAll:
			sp.Complete = sp.SignedCount == sp.TotalCount
		}
		result = append(result, sp)
	}
	return result, rows.Err()
}

// OpenFirstSequence creates and opens tasks for the lowest sequence_no of the
// document's workflow. Called immediately after document import.
func OpenFirstSequence(ctx context.Context, tx pgx.Tx, docID int64, templateID int64) error {
	var minSeq int
	err := tx.QueryRow(ctx,
		`SELECT MIN(sequence_no) FROM workflow_steps WHERE workflow_template_id=$1`, templateID,
	).Scan(&minSeq)
	if err != nil {
		return fmt.Errorf("find first sequence: %w", err)
	}
	return createTasksForSequence(ctx, tx, docID, templateID, minSeq)
}

// ── internal helpers ──────────────────────────────────────────────────────────

func isStepComplete(ctx context.Context, tx pgx.Tx, docID int64, seqNo int, ct ConditionType) (bool, error) {
	var signed, total int
	err := tx.QueryRow(ctx, `
		SELECT COUNT(*) FILTER (WHERE status='signed'),
		       COUNT(*)
		  FROM signature_tasks
		 WHERE document_id=$1 AND sequence_no=$2
	`, docID, seqNo).Scan(&signed, &total)
	if err != nil {
		return false, fmt.Errorf("step progress query: %w", err)
	}
	switch ct {
	case ConditionAnyOne:
		return signed >= 1, nil
	case ConditionAll:
		return signed == total && total > 0, nil
	default:
		// condition_type=3 (external): external sign sets task to 'signed' via ExternalSign path.
		return signed == total && total > 0, nil
	}
}

func isDocumentComplete(ctx context.Context, tx pgx.Tx, docID int64) (bool, error) {
	var incomplete int
	err := tx.QueryRow(ctx, `
		SELECT COUNT(*) FROM signature_tasks
		 WHERE document_id=$1 AND status NOT IN ('signed','skipped','cancelled','rejected')
	`, docID).Scan(&incomplete)
	if err != nil {
		return false, err
	}
	return incomplete == 0, nil
}

func openNextSequence(ctx context.Context, tx pgx.Tx, docID int64, completedSeq int) error {
	// Find the workflow template bound to this document.
	var templateID int64
	if err := tx.QueryRow(ctx,
		`SELECT workflow_template_id FROM documents WHERE id=$1`, docID,
	).Scan(&templateID); err != nil {
		return fmt.Errorf("find template: %w", err)
	}

	// Find the smallest sequence_no greater than completedSeq.
	var nextSeq int
	err := tx.QueryRow(ctx, `
		SELECT COALESCE(MIN(sequence_no), 0) FROM workflow_steps
		 WHERE workflow_template_id=$1 AND sequence_no > $2
	`, templateID, completedSeq).Scan(&nextSeq)
	if err != nil || nextSeq == 0 {
		return nil // no more sequences
	}

	// Create tasks for the next sequence.
	return createTasksForSequence(ctx, tx, docID, templateID, nextSeq)
}

func createTasksForSequence(ctx context.Context, tx pgx.Tx, docID int64, templateID int64, seqNo int) error {
	// Get steps for this sequence.
	rows, err := tx.Query(ctx, `
		SELECT s.id, s.condition_type, a.user_id
		  FROM workflow_steps s
		  JOIN workflow_step_assignees a ON a.workflow_step_id = s.id
		 WHERE s.workflow_template_id=$1 AND s.sequence_no=$2
		 ORDER BY s.id, a.display_order
	`, templateID, seqNo)
	if err != nil {
		return fmt.Errorf("load step assignees: %w", err)
	}
	defer rows.Close()

	type row struct {
		stepID        int64
		conditionType int16
		userID        int64
	}
	var assignees []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.stepID, &r.conditionType, &r.userID); err != nil {
			return err
		}
		assignees = append(assignees, r)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for _, a := range assignees {
		if _, err := tx.Exec(ctx, `
			INSERT INTO signature_tasks
			       (document_id, workflow_step_id, assigned_user_id, sequence_no, condition_type, status, opened_at)
			VALUES ($1, $2, $3, $4, $5, 'open', now())
		`, docID, a.stepID, a.userID, seqNo, a.conditionType); err != nil {
			return fmt.Errorf("create task: %w", err)
		}
	}
	return nil
}

func writeSignatureEvent(ctx context.Context, tx pgx.Tx, in SignInput, task Task) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO signature_events
		       (task_id, document_id, signer_type, signer_user_id, signer_name, action,
		        signature_image_hash, comment, consent_text, ip_address, user_agent,
		        session_id, request_id, signed_at)
		SELECT $1, $2, 'internal', $3, u.display_name, 'sign',
		       $4, $5, $6,
		       $7::inet, $8, $9, $10, now()
		  FROM users u WHERE u.id=$3
	`, task.ID, task.DocumentID, in.SignerUserID,
		in.SignatureImageHash, in.Comment, in.ConsentText,
		nullableString(in.IPAddress), in.UserAgent, in.SessionID, in.RequestID,
	)
	return err
}

func writeRejectEvent(ctx context.Context, tx pgx.Tx, in RejectInput, task Task) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO signature_events
		       (task_id, document_id, signer_type, signer_user_id, signer_name, action,
		        comment, ip_address, user_agent, request_id, signed_at)
		SELECT $1, $2, 'internal', $3, u.display_name, 'reject',
		       $4, $5::inet, $6, $7, now()
		  FROM users u WHERE u.id=$3
	`, task.ID, task.DocumentID, in.SignerUserID,
		in.Reason, nullableString(in.IPAddress), in.UserAgent, in.RequestID,
	)
	return err
}

func writeAuditLog(ctx context.Context, tx pgx.Tx, action, entityType string, entityID int64, actorID int64) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO audit_logs (actor_type, actor_id, action, entity_type, entity_id)
		VALUES ('user', $1::text, $2, $3, $4::text)
	`, actorID, action, entityType, entityID)
	return err
}

func nullableString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// ExternalSign validates and processes an external signer's signature.
func (e *Engine) ExternalSign(ctx context.Context, taskID int64, tokenHash string, in SignInput) error {
	tx, err := e.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// Validate external signer token.
	var extSignerID int64
	var tokenExpiry time.Time
	var extStatus string
	err = tx.QueryRow(ctx, `
		SELECT es.id, es.token_expires_at, es.status
		  FROM signature_tasks st
		  JOIN external_signers es ON es.id = st.external_signer_id
		 WHERE st.id=$1 AND es.token_hash=$2
		   FOR UPDATE OF es
	`, taskID, tokenHash).Scan(&extSignerID, &tokenExpiry, &extStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("invalid token for task %d", taskID)
	}
	if err != nil {
		return fmt.Errorf("load external signer: %w", err)
	}
	if time.Now().After(tokenExpiry) {
		return ErrExternalTokenExpired{ExpiresAt: tokenExpiry}
	}
	if extStatus != "pending" {
		return ErrStepAlreadyActioned{TaskID: taskID}
	}

	// Mark task signed and external signer signed.
	if _, err := tx.Exec(ctx,
		`UPDATE signature_tasks SET status='signed', completed_at=now(), version=version+1 WHERE id=$1`, taskID,
	); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE external_signers SET status='signed' WHERE id=$1`, extSignerID,
	); err != nil {
		return err
	}

	// Write event (external).
	var docID int64
	var seqNo int
	var ct ConditionType
	_ = tx.QueryRow(ctx,
		`SELECT document_id, sequence_no, condition_type FROM signature_tasks WHERE id=$1`, taskID,
	).Scan(&docID, &seqNo, &ct)

	task := Task{ID: taskID, DocumentID: docID, SequenceNo: seqNo, ConditionType: ct}
	if err := writeSignatureEvent(ctx, tx, in, task); err != nil {
		return err
	}

	// Evaluate step and doc completion.
	stepComplete, err := isStepComplete(ctx, tx, docID, seqNo, ct)
	if err != nil {
		return err
	}
	if stepComplete {
		if err := openNextSequence(ctx, tx, docID, seqNo); err != nil {
			return err
		}
		docComplete, err := isDocumentComplete(ctx, tx, docID)
		if err != nil {
			return err
		}
		if docComplete {
			_, _ = tx.Exec(ctx,
				`UPDATE documents SET status='completed', updated_at=now() WHERE id=$1`, docID)
		}
	}

	return tx.Commit(ctx)
}
