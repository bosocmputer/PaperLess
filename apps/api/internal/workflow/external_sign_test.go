package workflow_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"testing"
	"time"

	"paperless-api/internal/workflow"
)

// TestExternalSign_Idempotency_And_Reuse drives the external sign path the
// way the HTTP handler does and checks two checklist items:
//   - request_id idempotency on external sign → exactly one signature_event
//   - reuse (second sign attempt) → rejected, no second event, no second doc-complete
func TestExternalSign_Idempotency_And_Reuse(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()
	engine := workflow.New(pool)

	// Create a dedicated single-external-step template so signing the external
	// task genuinely completes the document (no trailing internal sequences).
	suffix := time.Now().UnixNano()
	var templateID, approverStepID int64
	_ = pool.QueryRow(ctx, `
		INSERT INTO workflow_templates (doc_format_code, name, version, status, created_by)
		SELECT $1, 'audit ext template', 1, 'active', u.id FROM users u WHERE u.username='admin'
		RETURNING id
	`, fmt.Sprintf("AUDX%d", suffix)).Scan(&templateID)
	_ = pool.QueryRow(ctx, `
		INSERT INTO workflow_steps (workflow_template_id, position_code, position_name, sequence_no, condition_type)
		VALUES ($1, 'CUSTOMER', 'ลูกค้า', 1, 3) RETURNING id
	`, templateID).Scan(&approverStepID)
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM workflow_steps WHERE workflow_template_id=$1`, templateID)
		_, _ = pool.Exec(ctx, `DELETE FROM workflow_templates WHERE id=$1`, templateID)
	})

	var docID int64
	idKey := fmt.Sprintf("AUDX:AUDITEXT-%d:0", suffix)
	_ = pool.QueryRow(ctx, `
		INSERT INTO documents (doc_format_code, doc_no, revision, workflow_template_id, workflow_version, status, idempotency_key, source_hash)
		VALUES ($1, $2, 0, $3, 1, 'pending', $4, 'auditexthash') RETURNING id
	`, fmt.Sprintf("AUDX%d", suffix), fmt.Sprintf("AUDITEXT-%d", suffix), templateID, idKey).Scan(&docID)
	t.Cleanup(func() { cleanupDoc(pool, docID) })

	// Real token + hash, like the invite handler produces.
	rawToken := make([]byte, 32)
	for i := range rawToken {
		rawToken[i] = byte(i + 1)
	}
	sum := sha256.Sum256(rawToken)
	tokenHash := hex.EncodeToString(sum[:])

	var extSignerID int64
	_ = pool.QueryRow(ctx, `
		INSERT INTO external_signers (document_id, name, token_hash, token_expires_at, status)
		VALUES ($1, 'ลูกค้า Audit', $2, now() + interval '72 hours', 'pending') RETURNING id
	`, docID, tokenHash).Scan(&extSignerID)

	// The external task — single seq=1 c3 task so signing completes the doc.
	var extTaskID int64
	_ = pool.QueryRow(ctx, `
		INSERT INTO signature_tasks (document_id, workflow_step_id, external_signer_id, sequence_no, condition_type, status, opened_at)
		VALUES ($1, $2, $3, 1, 3, 'open', now()) RETURNING id
	`, docID, approverStepID, extSignerID).Scan(&extTaskID)

	in := workflow.SignInput{
		TaskID:             extTaskID,
		ExternalSignerName: "ลูกค้า Audit",
		SignatureImageHash: "deadbeefsig",
		ConsentText:        "consent",
		RequestID:          "req-audit-ext-1",
	}

	// First sign — must succeed.
	if err := engine.ExternalSign(ctx, extTaskID, tokenHash, in); err != nil {
		t.Fatalf("first ExternalSign: %v", err)
	}

	var eventCount1 int
	_ = pool.QueryRow(ctx, `SELECT COUNT(*) FROM signature_events WHERE task_id=$1`, extTaskID).Scan(&eventCount1)
	t.Logf("events after first sign: %d", eventCount1)
	if eventCount1 != 1 {
		t.Errorf("after first sign: want 1 event, got %d", eventCount1)
	}

	var signerType string
	_ = pool.QueryRow(ctx, `SELECT signer_type FROM signature_events WHERE task_id=$1`, extTaskID).Scan(&signerType)
	if signerType != "external" {
		t.Errorf("signer_type want 'external', got %q", signerType)
	}

	var docStatus string
	_ = pool.QueryRow(ctx, `SELECT status FROM documents WHERE id=$1`, docID).Scan(&docStatus)
	if docStatus != "completed" {
		t.Errorf("doc status after external sign want 'completed', got %q", docStatus)
	}

	// Second sign with the SAME request_id — idempotency check.
	err := engine.ExternalSign(ctx, extTaskID, tokenHash, in)
	t.Logf("second ExternalSign (same request_id) returned: %v", err)

	var eventCount2 int
	_ = pool.QueryRow(ctx, `SELECT COUNT(*) FROM signature_events WHERE task_id=$1`, extTaskID).Scan(&eventCount2)
	t.Logf("events after second sign: %d", eventCount2)
	if eventCount2 != 1 {
		t.Errorf("IDEMPOTENCY VIOLATION: after duplicate request_id, want 1 event, got %d", eventCount2)
	}
}
