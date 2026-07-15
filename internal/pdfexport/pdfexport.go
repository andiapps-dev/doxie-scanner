// Package pdfexport renders scanned page images into PDF documents,
// either a single page or an arbitrary ordered sequence of pages (used
// both for "export this page as PDF" and for combining pages picked
// from any number of past scan jobs into one document). It knows
// nothing about jobs or storage — callers resolve whatever pages they
// want into image.Image values first; this package only turns images
// into PDF bytes.
package pdfexport

import (
	"bytes"
	"fmt"
	"image"
	"image/png"

	"github.com/go-pdf/fpdf"
)

// ScanDPI is the resolution this application always scans at. Each PDF
// page is sized to the source image's real physical dimensions at this
// DPI (e.g. a 2544px-wide scan becomes an 8.48in-wide page), rather than
// being stretched or squashed into a fixed Letter/A4 box.
const ScanDPI = 300.0

// pngEncode is a package-level indirection purely so tests can exercise
// encode-failure paths without needing a contrived image.Image value
// (see the equivalent pattern in internal/scanjobs).
var pngEncode = png.Encode

// SinglePagePDF renders one image as a single-page PDF.
func SinglePagePDF(img image.Image) ([]byte, error) {
	return CombinePagesPDF([]image.Image{img})
}

// CombinePagesPDF renders images, in the given order, into one
// multi-page PDF.
func CombinePagesPDF(images []image.Image) ([]byte, error) {
	if len(images) == 0 {
		return nil, fmt.Errorf("pdfexport: no images given")
	}

	pdf := fpdf.New("P", "in", "Letter", "")
	pdf.SetMargins(0, 0, 0)
	pdf.SetAutoPageBreak(false, 0)

	for i, img := range images {
		if img == nil {
			return nil, fmt.Errorf("pdfexport: page %d is nil", i)
		}
		bounds := img.Bounds()
		w := float64(bounds.Dx()) / ScanDPI
		h := float64(bounds.Dy()) / ScanDPI

		var buf bytes.Buffer
		if err := pngEncode(&buf, img); err != nil {
			return nil, fmt.Errorf("pdfexport: encode page %d: %w", i, err)
		}

		pdf.AddPageFormat("P", fpdf.SizeType{Wd: w, Ht: h})
		name := fmt.Sprintf("page%d", i)
		opts := fpdf.ImageOptions{ImageType: "PNG"}
		pdf.RegisterImageOptionsReader(name, opts, &buf)
		pdf.ImageOptions(name, 0, 0, w, h, false, opts, 0, "")
	}

	if err := pdf.Error(); err != nil {
		return nil, fmt.Errorf("pdfexport: %w", err)
	}

	var out bytes.Buffer
	if err := pdf.Output(&out); err != nil {
		return nil, fmt.Errorf("pdfexport: output: %w", err)
	}
	return out.Bytes(), nil
}
