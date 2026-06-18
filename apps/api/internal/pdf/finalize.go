package pdf

import (
	"context"
	"crypto/sha256"
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

	pdfBytes, err := BuildEvidencePage(input)
	if err != nil {
		return "", fmt.Errorf("build evidence page: %w", err)
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
