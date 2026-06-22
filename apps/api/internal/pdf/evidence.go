// Package pdf builds the signature-evidence page that is appended to the
// original PDF to produce the final, legally-evidenced document.
//
// Strategy (Phase 1 default): append a new evidence page rather than stamping
// at coordinates. Works for all doc_format_code values without needing SML
// coordinate config. Exact-coordinate stamping is a Phase 3 enhancement.
package pdf

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"time"

	"github.com/jung-kurt/gofpdf"
)

// SignerRecord is one signer's contribution to the evidence page.
type SignerRecord struct {
	Name          string
	Role          string
	SignerType    string // "internal" | "external"
	Action        string // "sign" | "skip"
	SignedAt      time.Time
	IPAddress     string
	SignatureHash string // sha-256 of the signature image bytes (NOT the image itself)
}

// EvidenceInput carries everything needed to build the evidence page.
type EvidenceInput struct {
	DocFormatCode  string
	DocNo          string
	Revision       int
	OriginalPDFHash string // sha-256 of the original PDF bytes
	Signers        []SignerRecord
	CompletedAt    time.Time
}

// VerificationCode is a short fingerprint displayed on the evidence page so
// anyone can verify the document chain. It is sha-256(docNo + originalHash + completedAt).
func VerificationCode(input EvidenceInput) string {
	raw := fmt.Sprintf("%s|%s|%s",
		input.DocNo,
		input.OriginalPDFHash,
		input.CompletedAt.UTC().Format(time.RFC3339),
	)
	sum := sha256.Sum256([]byte(raw))
	return fmt.Sprintf("%X", sum[:8]) // 16-char hex
}

// BuildEvidencePage generates a standalone PDF containing the evidence page.
// The caller appends its bytes to the original PDF (e.g. with pdfcpu or by
// using this as the second file in a merge).
//
// For Phase 1 we produce a self-contained PDF evidence document and store it
// alongside the original; the "final PDF" is the evidence doc itself (which
// references the original by hash). Full merge (original + evidence page as
// one file) is a Phase 2 enhancement once pdfcpu is added.
func BuildEvidencePage(input EvidenceInput) ([]byte, error) {
	pdf := gofpdf.New("P", "mm", "A4", "")
	pdf.SetTitle(fmt.Sprintf("Evidence — %s %s", input.DocFormatCode, input.DocNo), true)
	pdf.SetAuthor("PaperLess 1.0", true)
	pdf.AddPage()
	DrawEvidenceBody(pdf, input)

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, fmt.Errorf("render evidence PDF: %w", err)
	}
	return buf.Bytes(), nil
}

// DrawEvidenceBody draws the evidence content onto the current page of an
// mm-unit gofpdf document. Used standalone (BuildEvidencePage) and appended to
// the stamped original in BuildStampedFinal. The caller adds the page first.
func DrawEvidenceBody(pdf *gofpdf.Fpdf, input EvidenceInput) {
	registerThaiFont(pdf)

	// ── Header ────────────────────────────────────────────────────────────────
	pdf.SetFont("Sarabun", "B", 16)
	pdf.CellFormat(0, 12, "PaperLess — Signature Evidence", "", 1, "C", false, 0, "")

	pdf.SetFont("Sarabun", "", 10)
	pdf.CellFormat(0, 6, fmt.Sprintf("Document: %s  %s  (revision %d)", input.DocFormatCode, input.DocNo, input.Revision), "", 1, "C", false, 0, "")
	pdf.CellFormat(0, 6, fmt.Sprintf("Completed: %s", input.CompletedAt.UTC().Format("2006-01-02 15:04:05 UTC")), "", 1, "C", false, 0, "")

	verCode := VerificationCode(input)
	pdf.SetFont("Sarabun", "B", 10)
	pdf.CellFormat(0, 8, fmt.Sprintf("Verification Code: %s", verCode), "", 1, "C", false, 0, "")

	pdf.Ln(4)
	pdf.SetDrawColor(180, 180, 180)
	pdf.Line(10, pdf.GetY(), 200, pdf.GetY())
	pdf.Ln(4)

	// ── Original document hash ────────────────────────────────────────────────
	pdf.SetFont("Sarabun", "B", 9)
	pdf.Cell(50, 6, "Original PDF SHA-256:")
	pdf.SetFont("Courier", "", 8)
	pdf.MultiCell(0, 5, input.OriginalPDFHash, "", "L", false)
	pdf.Ln(2)

	// ── Signer table ──────────────────────────────────────────────────────────
	pdf.SetFont("Sarabun", "B", 10)
	pdf.CellFormat(0, 8, "Signers", "", 1, "L", false, 0, "")

	pdf.SetFont("Sarabun", "B", 9)
	pdf.SetFillColor(230, 230, 230)
	pdf.CellFormat(50, 7, "Name / Role", "1", 0, "C", true, 0, "")
	pdf.CellFormat(20, 7, "Type", "1", 0, "C", true, 0, "")
	pdf.CellFormat(20, 7, "Action", "1", 0, "C", true, 0, "")
	pdf.CellFormat(45, 7, "Signed At (UTC)", "1", 0, "C", true, 0, "")
	pdf.CellFormat(30, 7, "IP Address", "1", 0, "C", true, 0, "")
	pdf.CellFormat(0, 7, "Sig Hash", "1", 1, "C", true, 0, "")

	pdf.SetFont("Sarabun", "", 8)
	pdf.SetFillColor(255, 255, 255)
	for i, s := range input.Signers {
		fill := i%2 == 1
		if fill {
			pdf.SetFillColor(245, 245, 245)
		} else {
			pdf.SetFillColor(255, 255, 255)
		}
		signerType := s.SignerType
		if signerType == "" {
			signerType = "internal"
		}
		nameRole := fmt.Sprintf("%s\n(%s)", s.Name, s.Role)
		ts := ""
		if !s.SignedAt.IsZero() {
			ts = s.SignedAt.UTC().Format("2006-01-02 15:04:05")
		}
		sigHashShort := ""
		if len(s.SignatureHash) >= 16 {
			sigHashShort = s.SignatureHash[:16] + "..."
		} else {
			sigHashShort = s.SignatureHash
		}

		// Use MultiCell for the name/role column, single Cell for the rest.
		x := pdf.GetX()
		y := pdf.GetY()
		pdf.MultiCell(50, 5, nameRole, "1", "L", fill)
		newY := pdf.GetY()
		rowH := newY - y

		pdf.SetXY(x+50, y)
		pdf.CellFormat(20, rowH, signerType, "1", 0, "C", fill, 0, "")
		pdf.CellFormat(20, rowH, s.Action, "1", 0, "C", fill, 0, "")
		pdf.CellFormat(45, rowH, ts, "1", 0, "C", fill, 0, "")
		pdf.CellFormat(30, rowH, s.IPAddress, "1", 0, "C", fill, 0, "")
		pdf.CellFormat(0, rowH, sigHashShort, "1", 1, "C", fill, 0, "")
	}

	pdf.Ln(6)
	pdf.Line(10, pdf.GetY(), 200, pdf.GetY())
	pdf.Ln(4)

	// ── Legal text (พ.ร.บ. ธุรกรรมอิเล็กทรอนิกส์) ────────────────────────────
	pdf.SetFont("Sarabun", "", 7)
	legalTH := "เอกสารนี้ได้รับการลงลายมือชื่ออิเล็กทรอนิกส์ตามพระราชบัญญัติว่าด้วยธุรกรรมทางอิเล็กทรอนิกส์ พ.ศ. 2544 และที่แก้ไขเพิ่มเติม มีผลผูกพันทางกฎหมายเทียบเท่าลายมือชื่อลายลักษณ์อักษร"
	legalEN := "This document has been signed electronically in accordance with the Electronic Transactions Act B.E. 2544 (2001) and its amendments. The electronic signatures are legally binding."
	pdf.MultiCell(0, 4, legalTH, "", "L", false)
	pdf.MultiCell(0, 4, legalEN, "", "L", false)

	pdf.Ln(2)
	pdf.SetFont("Sarabun", "", 7)
	pdf.CellFormat(0, 5, fmt.Sprintf("Generated by PaperLess 1.0 — %s", time.Now().UTC().Format(time.RFC3339)), "", 1, "R", false, 0, "")
}
