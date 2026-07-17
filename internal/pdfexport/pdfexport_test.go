package pdfexport

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"io"
	"math/rand"
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

// noisyImage builds a smooth gradient with per-pixel jitter layered on
// top — a flat, solid-color, or periodic-pattern test image would
// actually compress *better* as PNG than JPEG (verified independently: a
// solid color and a deterministic multiplicative-hash "noise" pattern
// both favored PNG, since deflate could still exploit their underlying
// periodicity), which is backwards from how real scanned pages behave.
// Real scans are smooth document/photo content plus non-periodic CIS
// sensor noise; math/rand (fixed seed, so this stays deterministic)
// reproduces that non-periodic character, unlike a hash-based formula —
// this is what actually defeats PNG's predictive filters while JPEG's
// DCT still exploits the smooth underlying gradient.
func noisyImage(w, h int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	rng := rand.New(rand.NewSource(42))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			jitter := rng.Intn(31) - 15
			img.Set(x, y, color.NRGBA{
				R: clamp8(255*x/w + jitter),
				G: clamp8(255*y/h + jitter),
				B: clamp8(200 + jitter),
				A: 255,
			})
		}
	}
	return img
}

func clamp8(v int) uint8 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(v)
}

func countPageObjects(pdfBytes []byte) int {
	// Each page object is serialized with "/Type /Page" (no trailing
	// "s"); the document catalog's page tree root uses "/Type /Pages"
	// instead, so this substring search doesn't double-count it.
	return bytes.Count(pdfBytes, []byte("/Type /Page\n")) + bytes.Count(pdfBytes, []byte("/Type /Page/"))
}

func TestSinglePagePDF(t *testing.T) {
	img := testImage(300, 600, color.NRGBA{R: 255, A: 255}) // 1in x 2in at 300 DPI
	data, err := SinglePagePDF(img, FormatPNG)
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
	data, err := CombinePagesPDF(images, FormatPNG)
	if err != nil {
		t.Fatalf("CombinePagesPDF: %v", err)
	}
	if got := countPageObjects(data); got != 3 {
		t.Errorf("expected 3 page objects, got %d", got)
	}
}

func TestCombinePagesPDF_EmptyInput(t *testing.T) {
	if _, err := CombinePagesPDF(nil, FormatPNG); err == nil {
		t.Fatal("expected an error for no images")
	}
}

func TestCombinePagesPDF_NilImageInSlice(t *testing.T) {
	images := []image.Image{testImage(10, 10, color.NRGBA{A: 255}), nil}
	if _, err := CombinePagesPDF(images, FormatPNG); err == nil {
		t.Fatal("expected an error for a nil page")
	}
}

func TestCombinePagesPDF_EncodeError(t *testing.T) {
	original := pngEncode
	pngEncode = func(w io.Writer, img image.Image) error { return errors.New("simulated encode failure") }
	defer func() { pngEncode = original }()

	img := testImage(10, 10, color.NRGBA{A: 255})
	if _, err := CombinePagesPDF([]image.Image{img}, FormatPNG); err == nil {
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
	if _, err := CombinePagesPDF([]image.Image{img}, FormatPNG); err == nil {
		t.Fatal("expected an error")
	}
}

func TestCombinePagesPDF_PageSizeMatchesPhysicalDimensions(t *testing.T) {
	// 2544px wide at 300 DPI should be 8.48in, matching the real scan
	// width used throughout this project.
	img := testImage(2544, 100, color.NRGBA{A: 255})
	data, err := SinglePagePDF(img, FormatPNG)
	if err != nil {
		t.Fatalf("SinglePagePDF: %v", err)
	}
	// MediaBox is expressed in points (1in = 72pt); 8.48in = 610.56pt.
	if !bytes.Contains(data, []byte("610.")) {
		t.Errorf("expected a MediaBox width around 610.56pt for an 8.48in-wide page; PDF bytes: %s", data)
	}
}

func TestCombinePagesPDF_JPEGFormat(t *testing.T) {
	// A JPEG-embedded page should carry a JPEG SOI marker (\xFF\xD8\xFF)
	// rather than a PNG signature (\x89PNG), and should be meaningfully
	// smaller than the same content losslessly PNG-encoded — otherwise
	// the format choice isn't actually doing anything. Uses noisyImage,
	// not a flat/patterned testImage: a flat color or perfectly periodic
	// pattern actually compresses *better* as PNG (verified separately),
	// which would make this assertion backwards relative to real scans.
	img := noisyImage(400, 400)

	jpegData, err := SinglePagePDF(img, FormatJPEG)
	if err != nil {
		t.Fatalf("SinglePagePDF(FormatJPEG): %v", err)
	}
	if !bytes.Contains(jpegData, []byte{0xFF, 0xD8, 0xFF}) {
		t.Error("expected a JPEG SOI marker in the embedded stream")
	}
	if bytes.Contains(jpegData, []byte{0x89, 'P', 'N', 'G'}) {
		t.Error("did not expect a PNG signature when FormatJPEG was requested")
	}

	pngData, err := SinglePagePDF(img, FormatPNG)
	if err != nil {
		t.Fatalf("SinglePagePDF(FormatPNG): %v", err)
	}
	if len(jpegData) >= len(pngData) {
		t.Errorf("expected JPEG output (%d bytes) to be smaller than PNG output (%d bytes)", len(jpegData), len(pngData))
	}
}

func TestCombinePagesPDF_JPEGEncodeError(t *testing.T) {
	original := jpegEncode
	jpegEncode = func(w io.Writer, img image.Image) error { return errors.New("simulated encode failure") }
	defer func() { jpegEncode = original }()

	img := testImage(10, 10, color.NRGBA{A: 255})
	if _, err := CombinePagesPDF([]image.Image{img}, FormatJPEG); err == nil {
		t.Fatal("expected an error")
	}
}
