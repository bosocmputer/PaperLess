package pdf

import (
	"bytes"
	"fmt"
	"io"

	"github.com/jung-kurt/gofpdf"
	"github.com/jung-kurt/gofpdf/contrib/gofpdi"
)

const ptToMM = 25.4 / 72.0

// Stamp is one signature image to place on the original PDF.
// X/Y/W/H are normalized to the page (0..1), top-left origin. Page is 1-based.
type Stamp struct {
	Page int
	X    float64
	Y    float64
	W    float64
	H    float64
	PNG  []byte
}

// BuildStampedFinal produces the final PDF: the original pages with each
// signature image stamped into its configured box, followed by the evidence
// page. The document is mm-unit; the original is imported via gofpdf/contrib/gofpdi
// (one importer — using a second importer in the same doc collides template ids)
// and the evidence is drawn natively. Returns an error if the original cannot be
// imported, so callers can fall back to the evidence-only PDF.
func BuildStampedFinal(origBytes []byte, evidence EvidenceInput, stamps []Stamp) ([]byte, error) {
	if len(origBytes) == 0 {
		return nil, fmt.Errorf("empty original PDF")
	}

	pdf := gofpdf.New("P", "mm", "A4", "")
	pdf.SetAutoPageBreak(false, 0)

	imp := gofpdi.NewImporter()
	var rs io.ReadSeeker = bytes.NewReader(origBytes)
	first := imp.ImportPageFromStream(pdf, &rs, 1, "/MediaBox")
	sizes := imp.GetPageSizes()
	nPages := len(sizes)
	if nPages == 0 {
		return nil, fmt.Errorf("original PDF has no importable pages")
	}

	for i := 1; i <= nPages; i++ {
		tpl := first
		if i > 1 {
			tpl = imp.ImportPageFromStream(pdf, &rs, i, "/MediaBox")
		}
		box := sizes[i]["/MediaBox"]
		wmm, hmm := box["w"]*ptToMM, box["h"]*ptToMM
		orient := "P"
		if wmm > hmm {
			orient = "L"
		}
		pdf.AddPageFormat(orient, gofpdf.SizeType{Wd: wmm, Ht: hmm})
		imp.UseImportedTemplate(pdf, tpl, 0, 0, wmm, hmm)

		for j, st := range stamps {
			if st.Page != i || len(st.PNG) == 0 {
				continue
			}
			name := fmt.Sprintf("sig_p%d_%d", i, j)
			opt := gofpdf.ImageOptions{ImageType: "PNG"}
			pdf.RegisterImageOptionsReader(name, opt, bytes.NewReader(st.PNG))
			// Normalized top-left → mm. gofpdf image origin is top-left.
			pdf.ImageOptions(name, st.X*wmm, st.Y*hmm, st.W*wmm, st.H*hmm, false, opt, 0, "")
		}
	}

	// Evidence page — drawn natively (mm), re-enabling auto page-break so a long
	// signer table paginates.
	pdf.SetAutoPageBreak(true, 15)
	pdf.AddPage()
	DrawEvidenceBody(pdf, evidence)

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, fmt.Errorf("render stamped final: %w", err)
	}
	return buf.Bytes(), nil
}
