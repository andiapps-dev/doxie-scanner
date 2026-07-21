package ocr

import (
	"errors"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/disintegration/imaging"
	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

// testPage draws a small synthetic "document" — a bordered rectangle
// (unpaper's deskew edge-detection scans the page's outer edges, so a
// borderless canvas gives it nothing to detect skew from — verified
// empirically while prototyping this test) with a few lines of text —
// then upscales it 4x with bicubic interpolation, since basicfont's
// 7x13 bitmap glyphs are too small/blocky for tesseract to read
// reliably at their native size (also verified empirically; 3x was
// already fully legible in testing, 4x keeps a small margin without
// paying for the ~7x slower unpaper+tesseract pass an 8x image costs).
func testPage(t *testing.T, lines []string) *image.NRGBA {
	t.Helper()
	const w, h = 800, 400
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), image.White, image.Point{}, draw.Src)

	border := color.RGBA{80, 80, 80, 255}
	for x := 0; x < w; x++ {
		img.Set(x, 0, border)
		img.Set(x, h-1, border)
	}
	for y := 0; y < h; y++ {
		img.Set(0, y, border)
		img.Set(w-1, y, border)
	}

	d := &font.Drawer{
		Dst:  img,
		Src:  image.NewUniform(color.Black),
		Face: basicfont.Face7x13,
	}
	y := 60
	for _, line := range lines {
		d.Dot = fixed.Point26_6{X: fixed.I(40), Y: fixed.I(y)}
		d.DrawString(line)
		y += 60
	}

	big := imaging.Resize(img, w*4, h*4, imaging.CatmullRom)
	return big
}

func writeTestPNG(t *testing.T, img image.Image) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "page.png")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create test png: %v", err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatalf("encode test png: %v", err)
	}
	return path
}

func containsAll(text string, words []string) []string {
	var missing []string
	upper := strings.ToUpper(text)
	for _, w := range words {
		if !strings.Contains(upper, w) {
			missing = append(missing, w)
		}
	}
	return missing
}

// TestExtractText_CrookedScan is the primary test for this package: it
// reproduces the exact scenario the OCR feature exists for — a
// meaningfully skewed (not just cardinal-rotated) scan — and checks
// that unpaper's deskew step plus tesseract still recover the text. This
// runs the real binaries (no mocking): both are cheap to install and
// already required in CI, and this is the one boundary in the app that
// can't meaningfully be tested any other way (same reasoning as testing
// doxiedx400's scan pipeline against a fake BulkDevice for the one
// boundary that *can't* run for real in CI — hardware — versus running
// this one for real since it can).
func TestExtractText_CrookedScan(t *testing.T) {
	requireTools(t)

	words := []string{"HELLO", "WORLD", "TESTING", "SCAN", "DOCUMENT", "PAGE", "SAMPLE", "REPORT"}
	page := testPage(t, []string{"HELLO WORLD", "TESTING SCAN", "DOCUMENT PAGE", "SAMPLE REPORT"})

	// A few degrees is a realistic "fed in a little crooked" skew — well
	// within unpaper's default +/-5 degree deskew-scan-range.
	rotated := imaging.Rotate(page, 4, color.White)
	path := writeTestPNG(t, rotated)

	text, err := ExtractText(path, "eng")
	if err != nil {
		t.Fatalf("ExtractText: %v", err)
	}
	if missing := containsAll(text, words); len(missing) > 0 {
		t.Errorf("expected recognized text to contain %v, missing %v; got:\n%s", words, missing, text)
	}
}

func TestExtractText_JobNotFound(t *testing.T) {
	requireTools(t)

	_, err := ExtractText(filepath.Join(t.TempDir(), "does-not-exist.png"), "eng")
	if err == nil {
		t.Fatal("expected an error for a missing input file")
	}
}

func TestExtractText_InvalidLanguage(t *testing.T) {
	requireTools(t)

	// Content/legibility is irrelevant here (only the language-load
	// failure is under test) and unpaper still runs on it before
	// tesseract ever checks the language, so keep this tiny rather than
	// paying for a full testPage-sized deskew pass.
	img := image.NewNRGBA(image.Rect(0, 0, 100, 100))
	draw.Draw(img, img.Bounds(), image.White, image.Point{}, draw.Src)
	path := writeTestPNG(t, img)

	if _, err := ExtractText(path, "not-a-real-language-code"); err == nil {
		t.Fatal("expected an error for an unknown tesseract language")
	}
}

// requireTools skips (rather than fails) when unpaper/tesseract aren't
// installed, so `go test ./...` still works on a dev machine that
// hasn't installed them locally — CI always has both (see ci.yml).
func requireTools(t *testing.T) {
	t.Helper()
	for _, tool := range []string{"unpaper", "tesseract"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not installed locally; CI installs it (see ci.yml)", tool)
		}
	}
}

func TestExtractText_CorruptPNG(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-really-a-page.png")
	if err := os.WriteFile(path, []byte("this is not a png file"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ExtractText(path, "eng"); err == nil {
		t.Fatal("expected an error decoding a corrupt PNG")
	}
}

func TestExtractText_MkdirTempError(t *testing.T) {
	original := mkdirTemp
	mkdirTemp = func(dir, pattern string) (string, error) {
		return "", os.ErrPermission
	}
	defer func() { mkdirTemp = original }()

	page := testPage(t, []string{"HELLO WORLD"})
	path := writeTestPNG(t, page)

	if _, err := ExtractText(path, "eng"); err == nil {
		t.Fatal("expected an error when the temp dir can't be created")
	}
}

func TestWritePPM_CreateError(t *testing.T) {
	page := testPage(t, []string{"HELLO WORLD"})
	// A path inside a nonexistent parent directory always fails
	// os.Create, regardless of platform/permissions.
	badPath := filepath.Join(t.TempDir(), "no-such-dir", "x.ppm")
	if err := writePPM(badPath, page); err == nil {
		t.Fatal("expected an error creating the PPM file in a nonexistent directory")
	}
}

func TestRunUnpaper_ToolNotFound(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // an empty dir on $PATH: nothing resolves
	if err := runUnpaper("in.ppm", "out.ppm"); !errors.Is(err, ErrToolNotAvailable) {
		t.Fatalf("expected ErrToolNotAvailable, got %v", err)
	}
}

func TestRunTesseract_ToolNotFound(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if _, err := runTesseract("in.ppm", "eng"); !errors.Is(err, ErrToolNotAvailable) {
		t.Fatalf("expected ErrToolNotAvailable, got %v", err)
	}
}

func TestRunUnpaper_InvalidInput(t *testing.T) {
	requireTools(t)
	badPPM := filepath.Join(t.TempDir(), "not-a-ppm.ppm")
	if err := os.WriteFile(badPPM, []byte("not really a ppm"), 0o644); err != nil {
		t.Fatal(err)
	}
	outPath := filepath.Join(t.TempDir(), "out.ppm")
	if err := runUnpaper(badPPM, outPath); err == nil {
		t.Fatal("expected unpaper to fail on invalid input")
	}
}

func TestRunTesseract_InvalidInput(t *testing.T) {
	requireTools(t)
	badPPM := filepath.Join(t.TempDir(), "not-a-ppm.ppm")
	if err := os.WriteFile(badPPM, []byte("not really a ppm"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runTesseract(badPPM, "eng"); err == nil {
		t.Fatal("expected tesseract to fail on invalid input")
	}
}

// TestExtractText_WritePPMFails and TestExtractText_UnpaperFails cover
// ExtractText's own wrapping of writePPM's/runUnpaper's errors — a
// distinct branch from calling writePPM/runUnpaper directly (which the
// tests above already cover) since Go's statement coverage is per call
// site, not per callee.
func TestExtractText_WritePPMFails(t *testing.T) {
	original := mkdirTemp
	mkdirTemp = func(dir, pattern string) (string, error) {
		// A path that "succeeds" but doesn't actually exist: writePPM's
		// os.Create (called against a file inside this directory) fails,
		// exercising ExtractText's own error-wrapping around writePPM.
		return filepath.Join(t.TempDir(), "does-not-exist"), nil
	}
	defer func() { mkdirTemp = original }()

	page := testPage(t, []string{"HELLO WORLD"})
	path := writeTestPNG(t, page)

	if _, err := ExtractText(path, "eng"); err == nil {
		t.Fatal("expected an error when writePPM can't create its output file")
	}
}

func TestExtractText_UnpaperFails(t *testing.T) {
	page := testPage(t, []string{"HELLO WORLD"})
	path := writeTestPNG(t, page)

	t.Setenv("PATH", t.TempDir()) // neither unpaper nor tesseract resolve
	if _, err := ExtractText(path, "eng"); !errors.Is(err, ErrToolNotAvailable) {
		t.Fatalf("expected ErrToolNotAvailable from the runUnpaper step, got %v", err)
	}
}

type failAfterWriter struct {
	writesAllowed int
}

func (w *failAfterWriter) Write(p []byte) (int, error) {
	if w.writesAllowed <= 0 {
		return 0, errors.New("simulated write failure")
	}
	w.writesAllowed--
	return len(p), nil
}

func TestEncodePPM_HeaderWriteError(t *testing.T) {
	page := testPage(t, []string{"HELLO WORLD"})
	if err := encodePPM(&failAfterWriter{writesAllowed: 0}, page); err == nil {
		t.Fatal("expected an error when the header write fails")
	}
}

func TestEncodePPM_RowWriteError(t *testing.T) {
	page := testPage(t, []string{"HELLO WORLD"})
	// Allow exactly the header write (1) to succeed, then fail on the
	// first pixel-row write.
	if err := encodePPM(&failAfterWriter{writesAllowed: 1}, page); err == nil {
		t.Fatal("expected an error when a row write fails")
	}
}
