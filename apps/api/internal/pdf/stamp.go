package pdf

import (
	"bytes"
	"fmt"
	"io"

	"github.com/jung-kurt/gofpdf"
	"github.com/jung-kurt/gofpdf/contrib/gofpdi"
)

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
// page(s). Everything is assembled in one point-unit gofpdf document; the
// original and evidence are imported via gofpdi (their MediaBox sizes are
// preserved). Returns an error if the original cannot be imported — callers
// should fall back to the evidence-only PDF so the document stays usable.
func BuildStampedFinal(origBytes, evidenceBytes []byte, stamps []Stamp) ([]byte, error) {
	if len(origBytes) == 0 {
		return nil, fmt.Errorf("empty original PDF")
	}

	pdf := gofpdf.New("P", "pt", "A4", "")
	pdf.SetAutoPageBreak(false, 0)

	// ── Original pages + signature stamps ──
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
		w, h := box["w"], box["h"]
		orient := "P"
		if w > h {
			orient = "L"
		}
		pdf.AddPageFormat(orient, gofpdf.SizeType{Wd: w, Ht: h})
		imp.UseImportedTemplate(pdf, tpl, 0, 0, w, h)

		// Stamp signatures whose slot is on this page.
		for j, st := range stamps {
			if st.Page != i || len(st.PNG) == 0 {
				continue
			}
			name := fmt.Sprintf("sig_p%d_%d", i, j)
			opt := gofpdf.ImageOptions{ImageType: "PNG"}
			pdf.RegisterImageOptionsReader(name, opt, bytes.NewReader(st.PNG))
			// Normalized (top-left) → points. gofpdf image origin is also top-left.
			pdf.ImageOptions(name, st.X*w, st.Y*h, st.W*w, st.H*h, false, opt, 0, "")
		}
	}

	// ── Evidence page(s), imported from the existing evidence PDF ──
	if len(evidenceBytes) > 0 {
		impE := gofpdi.NewImporter()
		var rsE io.ReadSeeker = bytes.NewReader(evidenceBytes)
		te := impE.ImportPageFromStream(pdf, &rsE, 1, "/MediaBox")
		es := impE.GetPageSizes()
		for i := 1; i <= len(es); i++ {
			tpl := te
			if i > 1 {
				tpl = impE.ImportPageFromStream(pdf, &rsE, i, "/MediaBox")
			}
			box := es[i]["/MediaBox"]
			w, h := box["w"], box["h"]
			orient := "P"
			if w > h {
				orient = "L"
			}
			pdf.AddPageFormat(orient, gofpdf.SizeType{Wd: w, Ht: h})
			impE.UseImportedTemplate(pdf, tpl, 0, 0, w, h)
		}
	}

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, fmt.Errorf("render stamped final: %w", err)
	}
	return buf.Bytes(), nil
}
