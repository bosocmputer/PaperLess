package pdf

import (
	"bytes"
	"encoding/base64"
	"regexp"
	"testing"

	"github.com/jung-kurt/gofpdf"
)

// 1×1 PNG.
const onePxPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+M8AAAMBAQDJ/pLvAAAAAElFTkSuQmCC"

func testPNGBytes(t *testing.T) []byte {
	t.Helper()
	b, err := base64.StdEncoding.DecodeString(onePxPNG)
	if err != nil {
		t.Fatalf("decode png: %v", err)
	}
	return b
}

func makeTestPDF(t *testing.T, pages int) []byte {
	t.Helper()
	pdf := gofpdf.New("P", "mm", "A4", "")
	for i := 0; i < pages; i++ {
		pdf.AddPage()
		pdf.SetFont("Helvetica", "", 12)
		pdf.Cell(40, 10, "orig page")
	}
	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		t.Fatalf("make pdf: %v", err)
	}
	return buf.Bytes()
}

// countPageLeaves counts page-leaf objects ("/Type /Page" not "/Pages").
var pageLeafRe = regexp.MustCompile(`/Type\s*/Page[ />\r\n]`)

func countPageLeaves(pdf []byte) int {
	return len(pageLeafRe.FindAll(pdf, -1))
}

func TestBuildStampedFinal_StampsAndAppendsEvidence(t *testing.T) {
	orig := makeTestPDF(t, 1)
	stamps := []Stamp{{Page: 1, X: 0.1, Y: 0.8, W: 0.2, H: 0.05, PNG: testPNGBytes(t)}}

	out, err := BuildStampedFinal(orig, EvidenceInput{DocNo: "X1", DocFormatCode: "POP"}, stamps)
	if err != nil {
		t.Fatalf("BuildStampedFinal: %v", err)
	}
	if !bytes.HasPrefix(out, []byte("%PDF")) {
		t.Fatalf("output is not a PDF (prefix %q)", out[:min(4, len(out))])
	}
	if n := countPageLeaves(out); n < 2 {
		t.Errorf("want >=2 page leaves (original + evidence), got %d", n)
	}
	if len(out) <= len(orig) {
		t.Errorf("stamped+evidence output (%d) should exceed original (%d)", len(out), len(orig))
	}
}

func TestBuildStampedFinal_EmptyOriginalErrors(t *testing.T) {
	if _, err := BuildStampedFinal(nil, EvidenceInput{}, nil); err == nil {
		t.Error("want error on empty original PDF")
	}
}

func TestBuildStampedFinal_NoStampsStillMerges(t *testing.T) {
	orig := makeTestPDF(t, 2)
	out, err := BuildStampedFinal(orig, EvidenceInput{DocNo: "X2"}, nil)
	if err != nil {
		t.Fatalf("BuildStampedFinal: %v", err)
	}
	if n := countPageLeaves(out); n < 3 {
		t.Errorf("want >=3 page leaves (2 original + evidence), got %d", n)
	}
}
