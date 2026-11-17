package orient

import (
	"errors"
	"image"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/andiapps-dev/doxie-scanner/internal/pageedit"
	"github.com/disintegration/imaging"
)

func TestParse(t *testing.T) {
	good := "Page number: 0\nOrientation in degrees: 270\nRotate: 90\nOrientation confidence: 7.18\nScript: Latin\n"
	if r, ok := parse(good); !ok || r.Rotate != 90 || r.Confidence != 7.18 {
		t.Errorf("parse(good) = %+v, %v", r, ok)
	}
	for name, in := range map[string]string{
		"empty":          "",
		"no confidence":  "Rotate: 90\n",
		"no rotate":      "Orientation confidence: 5\n",
		"odd angle":      "Rotate: 45\nOrientation confidence: 5\n",
		"negative angle": "Rotate: -90\nOrientation confidence: 5\n",
		"not a number":   "Rotate: x\nOrientation confidence: 5\n",
		"bad confidence": "Rotate: 90\nOrientation confidence: x\n",
		"no colon":       "garbage line\n",
	} {
		if _, ok := parse(in); ok {
			t.Errorf("%s: parse accepted %q", name, in)
		}
	}
}

func withFakes(t *testing.T) {
	t.Helper()
	oldLook, oldRun, oldList, oldTmp := lookPath, runOSD, listLangs, mkdirTemp
	t.Cleanup(func() { lookPath, runOSD, listLangs, mkdirTemp = oldLook, oldRun, oldList, oldTmp })
	lookPath = func(string) (string, error) { return "/usr/bin/tesseract", nil }
}

func page(w, h int) *image.NRGBA { return image.NewNRGBA(image.Rect(0, 0, w, h)) }

func TestDetect_ReturnsConfidentAnswerAndShrinksBigPages(t *testing.T) {
	withFakes(t)
	var sawPath string
	runOSD = func(path string) ([]byte, error) {
		sawPath = path
		if _, err := os.Stat(path); err != nil {
			t.Errorf("tesseract was handed a missing file: %v", err)
		}
		return []byte("Rotate: 270\nOrientation confidence: 6.5\n"), nil
	}
	for _, size := range [][2]int{{3000, 2000}, {2000, 3000}, {400, 300}} {
		res, err := Detect(page(size[0], size[1]))
		if err != nil || res.Rotate != 270 || res.Confidence != 6.5 {
			t.Fatalf("%v: Detect = %+v, %v", size, res, err)
		}
	}
	if _, err := os.Stat(sawPath); err == nil {
		t.Error("the temporary page was not cleaned up")
	}
}

func TestDetect_UndetectedCases(t *testing.T) {
	withFakes(t)
	runOSD = func(string) ([]byte, error) { return nil, errors.New("exit 1: Too few characters") }
	if _, err := Detect(page(50, 50)); !errors.Is(err, ErrUndetected) {
		t.Errorf("tesseract failure: %v", err)
	}
	runOSD = func(string) ([]byte, error) { return []byte("nonsense"), nil }
	if _, err := Detect(page(50, 50)); !errors.Is(err, ErrUndetected) {
		t.Errorf("unparseable output: %v", err)
	}
	runOSD = func(string) ([]byte, error) {
		return []byte("Rotate: 90\nOrientation confidence: 0.4\n"), nil
	}
	if _, err := Detect(page(50, 50)); !errors.Is(err, ErrUndetected) {
		t.Errorf("low confidence: %v", err)
	}
}

func TestDetect_Failures(t *testing.T) {
	withFakes(t)
	lookPath = func(string) (string, error) { return "", exec.ErrNotFound }
	if _, err := Detect(page(10, 10)); !errors.Is(err, ErrToolNotAvailable) {
		t.Errorf("no tesseract: %v", err)
	}
	withFakes(t)
	mkdirTemp = func(string, string) (string, error) { return "", errors.New("no tmp") }
	if _, err := Detect(page(10, 10)); err == nil || errors.Is(err, ErrUndetected) {
		t.Errorf("temp dir failure: %v", err)
	}
	withFakes(t)
	mkdirTemp = func(string, string) (string, error) { return "/nonexistent-dir-for-test", nil }
	if _, err := Detect(page(10, 10)); err == nil || errors.Is(err, ErrUndetected) {
		t.Errorf("write failure: %v", err)
	}
}

func TestAvailable(t *testing.T) {
	withFakes(t)
	listLangs = func() ([]byte, error) {
		return []byte("List of available languages in \"/x\" (2):\neng\nosd\n"), nil
	}
	if !Available() {
		t.Error("tesseract with osd should be available")
	}
	listLangs = func() ([]byte, error) { return []byte("List of available languages (1):\neng\n"), nil }
	if Available() {
		t.Error("without osd it must not be available")
	}
	listLangs = func() ([]byte, error) { return nil, errors.New("boom") }
	if Available() {
		t.Error("a failing tesseract must not be available")
	}
	lookPath = func(string) (string, error) { return "", exec.ErrNotFound }
	if Available() {
		t.Error("no tesseract must not be available")
	}
}

// turn is img turned clockwise by degrees (0 leaves it as it is).
func turn(img image.Image, degrees int) image.Image {
	if degrees == 0 {
		return img
	}
	out, err := pageedit.Rotate(img, degrees)
	if err != nil {
		panic(err)
	}
	return out
}

// textPage is a generated letter-size page of ordinary text, testdata/
// text-upright.png. A page drawn with a bitmap font is too blocky for the
// orientation detector to tell up from down, so this is a real rendering.
func textPage(t *testing.T) *image.NRGBA {
	t.Helper()
	f, err := os.Open("testdata/text-upright.png")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	return imaging.Clone(img)
}

// TestDetect_RealTesseract runs the real detector on a page turned each
// way. It is skipped where tesseract or its osd data isn't installed.
func TestDetect_RealTesseract(t *testing.T) {
	if !Available() {
		t.Skip("tesseract with osd data not installed")
	}
	upright := textPage(t)
	for _, turned := range []int{0, 90, 180, 270} {
		res, err := Detect(turn(upright, turned))
		if err != nil {
			t.Fatalf("turned %d: %v", turned, err)
		}
		if want := (360 - turned) % 360; res.Rotate != want {
			t.Errorf("page turned %d clockwise: Rotate = %d, want %d", turned, res.Rotate, want)
		}
	}
	// Turning by the answer gives back an upright page.
	sideways := turn(upright, 90)
	res, _ := Detect(sideways)
	back, err := Detect(turn(sideways, res.Rotate))
	if err != nil || back.Rotate != 0 {
		t.Errorf("after correcting, Rotate = %d (%v), want 0", back.Rotate, err)
	}
}

func TestDetect_GivesUpOnATesseractThatHangs(t *testing.T) {
	// A stand-in tesseract on PATH that never returns. "exec" so that the
	// process the timeout kills is the sleep itself.
	dir := t.TempDir()
	script := "#!/bin/sh\nexec /bin/sleep 30\n"
	if err := os.WriteFile(filepath.Join(dir, "tesseract"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	old := osdTimeout
	osdTimeout = 300 * time.Millisecond
	defer func() { osdTimeout = old }()

	start := time.Now()
	_, err := Detect(image.NewNRGBA(image.Rect(0, 0, 40, 40)))
	if !errors.Is(err, ErrUndetected) {
		t.Errorf("err = %v, want ErrUndetected for a run that timed out", err)
	}
	took := time.Since(start)
	if took < osdTimeout-50*time.Millisecond {
		t.Errorf("took only %v: the stand-in never hung, so this proves nothing", took)
	}
	if took > 10*time.Second {
		t.Errorf("took %v: the hung run was not cut off", took)
	}
}
