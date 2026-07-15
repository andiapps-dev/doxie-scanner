package pdfexport

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"io"
	"testing"
)

func testImage(w, h int, c color.NRGBA) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	return img
}

func countPageObjects(pdfBytes []byte) int {
	// Each page object is serialized with "/Type /Page" (no trailing
	// "s"); the document catalog's page tree root uses "/Type /Pages"
	// instead, so this substring search doesn't double-count it.
	return bytes.Count(pdfBytes, []byte("/Type /Page\n")) + bytes.Count(pdfBytes, []byte("/Type /Page/"))
}

func TestSinglePagePDF(t *testing.T) {
	img := testImage(300, 600, color.NRGBA{R: 255, A: 255}) // 1in x 2in at 300 DPI
	data, err := SinglePagePDF(img)
	if err != nil {
		t.Fatalf("SinglePagePDF: %v", err)
	}
	if !bytes.HasPrefix(data, []byte("%PDF")) {
		t.Fatal("output doesn't look like a PDF")
	}
	if got := countPageObjects(data); got != 1 {
		t.Errorf("expected 1 page object, got %d", got)
	}
}

func TestCombinePagesPDF_MultiplePages(t *testing.T) {
	images := []image.Image{
		testImage(300, 600, color.NRGBA{R: 255, A: 255}),
		testImage(600, 300, color.NRGBA{G: 255, A: 255}),
		testImage(300, 300, color.NRGBA{B: 255, A: 255}),
	}
	data, err := CombinePagesPDF(images)
	if err != nil {
		t.Fatalf("CombinePagesPDF: %v", err)
	}
	if got := countPageObjects(data); got != 3 {
		t.Errorf("expected 3 page objects, got %d", got)
	}
}

func TestCombinePagesPDF_EmptyInput(t *testing.T) {
	if _, err := CombinePagesPDF(nil); err == nil {
		t.Fatal("expected an error for no images")
	}
}

func TestCombinePagesPDF_NilImageInSlice(t *testing.T) {
	images := []image.Image{testImage(10, 10, color.NRGBA{A: 255}), nil}
	if _, err := CombinePagesPDF(images); err == nil {
		t.Fatal("expected an error for a nil page")
	}
}

func TestCombinePagesPDF_EncodeError(t *testing.T) {
	original := pngEncode
	pngEncode = func(w io.Writer, img image.Image) error { return errors.New("simulated encode failure") }
	defer func() { pngEncode = original }()

	img := testImage(10, 10, color.NRGBA{A: 255})
	if _, err := CombinePagesPDF([]image.Image{img}); err == nil {
		t.Fatal("expected an error")
	}
}

func TestCombinePagesPDF_FpdfInternalError(t *testing.T) {
	// Writing non-PNG garbage where fpdf expects a valid PNG makes
	// fpdf's own internal image parser set its error state, which
	// CombinePagesPDF must surface rather than silently producing a
	// broken PDF.
	original := pngEncode
	pngEncode = func(w io.Writer, img image.Image) error {
		_, err := w.Write([]byte("not a real png"))
		return err
	}
	defer func() { pngEncode = original }()

	img := testImage(10, 10, color.NRGBA{A: 255})
	if _, err := CombinePagesPDF([]image.Image{img}); err == nil {
		t.Fatal("expected an error")
	}
}

func TestCombinePagesPDF_PageSizeMatchesPhysicalDimensions(t *testing.T) {
	// 2544px wide at 300 DPI should be 8.48in, matching the real scan
	// width used throughout this project.
	img := testImage(2544, 100, color.NRGBA{A: 255})
	data, err := SinglePagePDF(img)
	if err != nil {
		t.Fatalf("SinglePagePDF: %v", err)
	}
	// MediaBox is expressed in points (1in = 72pt); 8.48in = 610.56pt.
	if !bytes.Contains(data, []byte("610.")) {
		t.Errorf("expected a MediaBox width around 610.56pt for an 8.48in-wide page; PDF bytes: %s", data)
	}
}
