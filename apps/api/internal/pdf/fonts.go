package pdf

import (
	_ "embed"

	"github.com/jung-kurt/gofpdf"
)

// Sarabun (Thai government standard) embedded into the binary so the evidence
// page renders Thai correctly. gofpdf's core fonts (Helvetica/Courier) are
// Latin-only and turn Thai + punctuation into mojibake.
//
//go:embed fonts/Sarabun-Regular.ttf
var sarabunRegular []byte

//go:embed fonts/Sarabun-Bold.ttf
var sarabunBold []byte

// registerThaiFont registers Sarabun (regular + bold) as a UTF-8 font on the
// document. Call once per *gofpdf.Fpdf before using SetFont("Sarabun", ...).
func registerThaiFont(pdf *gofpdf.Fpdf) {
	pdf.AddUTF8FontFromBytes("Sarabun", "", sarabunRegular)
	pdf.AddUTF8FontFromBytes("Sarabun", "B", sarabunBold)
}
