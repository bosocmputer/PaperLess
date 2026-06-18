package handlers

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"paperless-api/internal/config"
	"paperless-api/internal/storage"
)

// TestFinalize_RecoveryRoundTrip is the end-to-end proof of the Step 2b feature
// that the delivery's unit tests did NOT cover: the *happy* recovery path.
//
// The delivered tests prove the guards (403/409) and the failure case (nil store
// → 500), but the plan's "Done when" is: "the retry endpoint regenerates the PDF
// once storage is back". That requires a REAL working store. This test:
//
//  1. Seeds a completed doc with a signature_event but NO final_pdf (the state
//     after a completion where storage was down).
//  2. Asserts GET /file/final → 409 pdf_generation_pending (not 404).
//  3. Calls POST /finalize against a real MinIO → 200, regenerates the PDF.
//  4. Asserts GET /file/final now → 200 with a valid %PDF body.
//  5. Calls POST /finalize AGAIN → still 200, and asserts exactly ONE final_pdf
//     row (idempotent — no duplicate file).
//
// Gated on PAPERLESS_TEST_DB and MINIO_TEST_ENDPOINT. Skips cleanly if MinIO is
// not provided (the rest of the suite still covers the guards without it).
func TestFinalize_RecoveryRoundTrip(t *testing.T) {
	pool := validationPool(t)

	endpoint := os.Getenv("MINIO_TEST_ENDPOINT")
	if endpoint == "" {
		t.Skip("MINIO_TEST_ENDPOINT not set — skipping real-storage recovery round-trip")
	}

	ctx := context.Background()
	suffix := time.Now().UnixNano()
	bucket := fmt.Sprintf("paperless-audit-%d", suffix)

	cfg := &config.Config{}
	cfg.Storage.Endpoint = endpoint
	cfg.Storage.AccessKey = getEnvOr("MINIO_TEST_ACCESS_KEY", "minioadmin")
	cfg.Storage.SecretKey = getEnvOr("MINIO_TEST_SECRET_KEY", "minioadmin")
	cfg.Storage.Bucket = bucket
	cfg.Storage.UseSSL = false

	store, err := storage.New(cfg)
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	if err := store.EnsureBucket(ctx); err != nil {
		t.Fatalf("EnsureBucket: %v", err)
	}

	// ── Seed: completed doc + one signature_event, but NO final_pdf row. ──
	var templateID, stepID int64
	_ = pool.QueryRow(ctx, `
		INSERT INTO workflow_templates (doc_format_code, name, version, status, created_by)
		SELECT $1, 'recovery test', 1, 'active', u.id FROM users u WHERE u.username='admin' RETURNING id
	`, fmt.Sprintf("RCV%d", suffix)).Scan(&templateID)
	_ = pool.QueryRow(ctx, `
		INSERT INTO workflow_steps (workflow_template_id, position_code, position_name, sequence_no, condition_type)
		VALUES ($1, 'CUSTOMER', 'ลูกค้า', 1, 1) RETURNING id
	`, templateID).Scan(&stepID)

	var docID int64
	_ = pool.QueryRow(ctx, `
		INSERT INTO documents (doc_format_code, doc_no, revision, workflow_template_id, workflow_version,
		                       status, idempotency_key, source_hash)
		VALUES ($1, $2, 0, $3, 1, 'completed', $4, 'rcvhash') RETURNING id
	`, fmt.Sprintf("RCV%d", suffix), fmt.Sprintf("RD-%d", suffix), templateID,
		fmt.Sprintf("RCV:%d:0", suffix)).Scan(&docID)

	var taskID int64
	_ = pool.QueryRow(ctx, `
		INSERT INTO signature_tasks (document_id, workflow_step_id, sequence_no, condition_type, status)
		VALUES ($1, $2, 1, 1, 'signed') RETURNING id
	`, docID, stepID).Scan(&taskID)

	// A signature_event so the evidence page has a signer row to render.
	_, err = pool.Exec(ctx, `
		INSERT INTO signature_events (document_id, task_id, signer_name, signer_type, action, signed_at)
		VALUES ($1, $2, 'ผู้ทดสอบ', 'internal', 'sign', now())
	`, docID, taskID)
	if err != nil {
		t.Fatalf("seed signature_event: %v", err)
	}

	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM signature_events WHERE document_id=$1`, docID)
		_, _ = pool.Exec(ctx, `DELETE FROM document_files WHERE document_id=$1`, docID)
		_, _ = pool.Exec(ctx, `DELETE FROM signature_tasks WHERE document_id=$1`, docID)
		_, _ = pool.Exec(ctx, `DELETE FROM documents WHERE id=$1`, docID)
		_, _ = pool.Exec(ctx, `DELETE FROM workflow_steps WHERE workflow_template_id=$1`, templateID)
		_, _ = pool.Exec(ctx, `DELETE FROM workflow_templates WHERE id=$1`, templateID)
	})

	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := NewDocumentHandler(pool, store, nil, zap.NewNop())
	r.GET("/documents/:id/file/final", fakeAuth(1, "document_admin"), h.DownloadFinal)
	r.POST("/documents/:id/finalize", fakeAuth(1, "document_admin"), h.Finalize)

	// ── 1. Before recovery: 409 pdf_generation_pending. ──
	{
		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", fmt.Sprintf("/documents/%d/file/final", docID), nil)
		r.ServeHTTP(w, req)
		t.Logf("pre-finalize DownloadFinal → %d %s", w.Code, w.Body.String())
		if w.Code != http.StatusConflict {
			t.Fatalf("pre-finalize: want 409, got %d", w.Code)
		}
		if got := errorCode(t, w.Body.String()); got != "pdf_generation_pending" {
			t.Fatalf("pre-finalize code: want pdf_generation_pending, got %q", got)
		}
	}

	// ── 2. POST /finalize → regenerates. ──
	{
		w := httptest.NewRecorder()
		req := httptest.NewRequest("POST", fmt.Sprintf("/documents/%d/finalize", docID), nil)
		r.ServeHTTP(w, req)
		t.Logf("finalize #1 → %d %s", w.Code, w.Body.String())
		if w.Code != http.StatusOK {
			t.Fatalf("finalize #1: want 200, got %d", w.Code)
		}
	}

	// ── 3. After recovery: 200 with valid %PDF body. ──
	{
		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", fmt.Sprintf("/documents/%d/file/final", docID), nil)
		r.ServeHTTP(w, req)
		t.Logf("post-finalize DownloadFinal → %d (%d bytes)", w.Code, w.Body.Len())
		if w.Code != http.StatusOK {
			t.Fatalf("post-finalize: want 200, got %d (%s)", w.Code, w.Body.String())
		}
		if body := w.Body.Bytes(); len(body) < 4 || string(body[:4]) != "%PDF" {
			t.Fatalf("post-finalize: body is not a PDF (first bytes: %q)", body[:min(8, len(body))])
		}
	}

	// ── 4. Idempotency: finalize again → still 200, exactly ONE final_pdf row. ──
	{
		w := httptest.NewRecorder()
		req := httptest.NewRequest("POST", fmt.Sprintf("/documents/%d/finalize", docID), nil)
		r.ServeHTTP(w, req)
		t.Logf("finalize #2 (idempotent) → %d %s", w.Code, w.Body.String())
		if w.Code != http.StatusOK {
			t.Fatalf("finalize #2: want 200, got %d", w.Code)
		}
	}
	var fileCount int
	_ = pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM document_files WHERE document_id=$1 AND file_type='final_pdf'`, docID,
	).Scan(&fileCount)
	if fileCount != 1 {
		t.Fatalf("idempotency violated: want exactly 1 final_pdf row, got %d", fileCount)
	}
}

func getEnvOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
