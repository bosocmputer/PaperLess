package pdf

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"paperless-api/internal/storage"
)

// FinalizeDocument generates the signature-evidence PDF for a completed document,
// stores it in MinIO, and records the file in document_files.
//
// This is idempotent: if a final_pdf already exists for the document, it returns
// the existing object_key without regenerating.
func FinalizeDocument(ctx context.Context, pool *pgxpool.Pool, store *storage.Client, docID int64) (objectKey string, err error) {
	// Idempotency: check if final_pdf already stored.
	var existing string
	if e := pool.QueryRow(ctx,
		`SELECT object_key FROM document_files WHERE document_id=$1 AND file_type='final_pdf' LIMIT 1`, docID,
	).Scan(&existing); e == nil {
		return existing, nil
	}

	// Load document metadata.
	var docFormatCode, docNo string
	var revision int
	var updatedAt time.Time
	if err := pool.QueryRow(ctx,
		`SELECT doc_format_code, doc_no, revision, updated_at FROM documents WHERE id=$1`, docID,
	).Scan(&docFormatCode, &docNo, &revision, &updatedAt); err != nil {
		return "", fmt.Errorf("load document: %w", err)
	}

	// Load original PDF hash.
	var originalHash string
	_ = pool.QueryRow(ctx,
		`SELECT COALESCE(file_hash, '') FROM document_files WHERE document_id=$1 AND file_type='original_pdf' LIMIT 1`, docID,
	).Scan(&originalHash)

	// Load signers from signature_events.
	rows, err := pool.Query(ctx, `
		SELECT se.signer_name, COALESCE(r.code,'') AS role,
		       se.signer_type, se.action, se.signed_at,
		       COALESCE(host(se.ip_address),'') AS ip,
		       COALESCE(se.signature_image_hash,'') AS sig_hash
		  FROM signature_events se
		  LEFT JOIN signature_tasks st ON st.id = se.task_id
		  LEFT JOIN workflow_steps ws ON ws.id = st.workflow_step_id
		  LEFT JOIN users u ON u.id = se.signer_user_id
		  LEFT JOIN user_roles ur ON ur.user_id = u.id
		  LEFT JOIN roles r ON r.id = ur.role_id
		 WHERE se.document_id=$1
		 ORDER BY se.signed_at
	`, docID)
	if err != nil {
		return "", fmt.Errorf("load signature events: %w", err)
	}
	defer rows.Close()

	var signers []SignerRecord
	for rows.Next() {
		var s SignerRecord
		if err := rows.Scan(&s.Name, &s.Role, &s.SignerType, &s.Action, &s.SignedAt, &s.IPAddress, &s.SignatureHash); err != nil {
			return "", fmt.Errorf("scan signer: %w", err)
		}
		signers = append(signers, s)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}

	input := EvidenceInput{
		DocFormatCode:   docFormatCode,
		DocNo:           docNo,
		Revision:        revision,
		OriginalPDFHash: originalHash,
		Signers:         signers,
		CompletedAt:     updatedAt,
	}

	evidenceBytes, err := BuildEvidencePage(input)
	if err != nil {
		return "", fmt.Errorf("build evidence page: %w", err)
	}

	// Final PDF = original pages with signatures stamped into their configured
	// boxes, then the evidence page. If the original can't be loaded/imported or
	// has no configured slots, fall back to the evidence-only PDF so the document
	// stays usable (no signing path is blocked by PDF assembly).
	pdfBytes := evidenceBytes
	if orig, oerr := loadOriginalBytes(ctx, pool, store, docID); oerr == nil && len(orig) > 0 {
		if stamps, serr := gatherStamps(ctx, pool, store, docID); serr == nil {
			if merged, merr := BuildStampedFinal(orig, input, stamps); merr == nil && len(merged) > 0 {
				pdfBytes = merged
			}
		}
	}

	// Store in MinIO.
	objectKey = fmt.Sprintf("documents/%d/final.pdf", docID)
	if err := store.Put(ctx, objectKey, "application/pdf",
		bytesReader(pdfBytes), int64(len(pdfBytes)),
	); err != nil {
		return "", fmt.Errorf("store final PDF: %w", err)
	}

	// Record in document_files.
	hash := sha256.Sum256(pdfBytes)
	_, err = pool.Exec(ctx, `
		INSERT INTO document_files (document_id, file_type, object_key, file_hash, mime_type, size_bytes)
		VALUES ($1, 'final_pdf', $2, $3, 'application/pdf', $4)
		ON CONFLICT DO NOTHING
	`, docID, objectKey, fmt.Sprintf("%x", hash), int64(len(pdfBytes)))
	if err != nil {
		return "", fmt.Errorf("record final PDF: %w", err)
	}

	return objectKey, nil
}

// loadOriginalBytes fetches the original PDF bytes from object storage.
func loadOriginalBytes(ctx context.Context, pool *pgxpool.Pool, store *storage.Client, docID int64) ([]byte, error) {
	var key string
	if err := pool.QueryRow(ctx,
		`SELECT object_key FROM document_files WHERE document_id=$1 AND file_type='original_pdf' LIMIT 1`, docID,
	).Scan(&key); err != nil {
		return nil, err
	}
	rc, _, err := store.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

// gatherStamps collects, for each signed event that has both a stored signature
// image and a configured slot on its step, the image bytes + normalized box.
func gatherStamps(ctx context.Context, pool *pgxpool.Pool, store *storage.Client, docID int64) ([]Stamp, error) {
	rows, err := pool.Query(ctx, `
		SELECT df.object_key, ws.signature_slot
		  FROM signature_events se
		  JOIN signature_tasks st ON st.id = se.task_id
		  JOIN workflow_steps ws ON ws.id = st.workflow_step_id
		  JOIN document_files df ON df.id = se.signature_file_id
		 WHERE se.document_id=$1
		   AND se.action='sign'
		   AND se.signature_file_id IS NOT NULL
		   AND ws.signature_slot IS NOT NULL
	`, docID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type slot struct {
		Page int     `json:"page"`
		X    float64 `json:"x"`
		Y    float64 `json:"y"`
		W    float64 `json:"w"`
		H    float64 `json:"h"`
	}
	var stamps []Stamp
	for rows.Next() {
		var key string
		var slotJSON []byte
		if err := rows.Scan(&key, &slotJSON); err != nil {
			return nil, err
		}
		var s slot
		if err := json.Unmarshal(slotJSON, &s); err != nil || s.Page < 1 {
			continue // skip malformed slot rather than fail the whole finalize
		}
		rc, _, gerr := store.Get(ctx, key)
		if gerr != nil {
			continue
		}
		img, rerr := io.ReadAll(rc)
		rc.Close()
		if rerr != nil || len(img) == 0 {
			continue
		}
		stamps = append(stamps, Stamp{Page: s.Page, X: s.X, Y: s.Y, W: s.W, H: s.H, PNG: img})
	}
	return stamps, rows.Err()
}

type bytesReadCloser struct{ *bytesReaderImpl }

type bytesReaderImpl struct {
	data []byte
	pos  int
}

func bytesReader(b []byte) io.Reader {
	return &bytesReaderImpl{data: b}
}

func (r *bytesReaderImpl) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	n := copy(p, r.data[r.pos:])
	r.pos += n
	return n, nil
}
