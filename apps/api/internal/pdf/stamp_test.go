package pdf

import (
	"bytes"
	_ "embed"
	"encoding/base64"
	"regexp"
	"testing"
)

// Real customer sample PDF — gofpdi imports it cleanly (gofpdf-generated PDFs
// do not import reliably, so we use a real one as the fixture).
//
//go:embed testdata/sample_po.pdf
var samplePO []byte

// 1×1 PNG signature.
const onePxPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+M8AAAMBAQDJ/pLvAAAAAElFTkSuQmCC"

func testPNGBytes(t *testing.T) []byte {
	t.Helper()
	b, err := base64.StdEncoding.DecodeString(onePxPNG)
	if err != nil {
		t.Fatalf("decode png: %v", err)
	}
	return b
}

// countPageLeaves counts page-leaf objects ("/Type /Page" but not "/Pages").
var pageLeafRe = regexp.MustCompile(`/Type\s*/Page[ />\r\n]`)

func countPageLeaves(pdf []byte) int { return len(pageLeafRe.FindAll(pdf, -1)) }

func TestBuildStampedFinal_StampsAndAppendsEvidence(t *testing.T) {
	stamps := []Stamp{{Page: 1, X: 0.1, Y: 0.8, W: 0.2, H: 0.05, PNG: testPNGBytes(t)}}

	out, err := BuildStampedFinal(samplePO, EvidenceInput{DocNo: "X1", DocFormatCode: "POP"}, stamps)
	if err != nil {
		t.Fatalf("BuildStampedFinal: %v", err)
	}
	if !bytes.HasPrefix(out, []byte("%PDF")) {
		t.Fatalf("output is not a PDF")
	}
	// original (1 page) + evidence (1 page) → at least 2 page leaves.
	if n := countPageLeaves(out); n < 2 {
		t.Errorf("want >=2 page leaves (original + evidence), got %d", n)
	}
}

func TestBuildStampedFinal_EmptyOriginalErrors(t *testing.T) {
	if _, err := BuildStampedFinal(nil, EvidenceInput{}, nil); err == nil {
		t.Error("want error on empty original PDF")
	}
}

// An unparseable PDF must produce an error (recovered panic), never crash —
// finalize relies on this to fall back to the evidence-only document.
func TestBuildStampedFinal_GarbageOriginalRecovers(t *testing.T) {
	if _, err := BuildStampedFinal([]byte("%PDF-1.4 this is not a real pdf body"), EvidenceInput{DocNo: "G1"}, nil); err == nil {
		t.Error("want error (recovered) on unparseable PDF, got nil")
	}
}
