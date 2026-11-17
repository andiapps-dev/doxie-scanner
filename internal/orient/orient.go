// Package orient finds which way up a scanned page is, from the text on
// it, and turns it upright. It asks tesseract's orientation detector
// (--psm 0 with the "osd" data), which reads the page's text rather than
// guessing from its shape, so it copes with a sheet that fed in sideways
// or upside down. It is a subprocess boundary like internal/ocr, because
// there's no pure-Go equivalent.
//
// A page with little or no text (a blank back, a photo) gives no
// confident answer; Detect reports that as ErrUndetected and the caller
// leaves the page alone.
package orient

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/disintegration/imaging"
)

// ErrToolNotAvailable means tesseract or its "osd" data isn't installed.
var ErrToolNotAvailable = errors.New("orient: tesseract with the osd language data is not installed")

// ErrUndetected means the page doesn't have enough text to tell, or the
// detector wasn't confident enough to act on.
var ErrUndetected = errors.New("orient: could not tell which way up the page is")

// Result is a detected orientation.
type Result struct {
	// Rotate is the clockwise turn, 0, 90, 180 or 270 degrees, that makes
	// the text upright.
	Rotate int
	// Confidence is tesseract's own score. Measured on real scans it was
	// 2 to 16 for correct answers.
	Confidence float64
}

// Func is the detector's signature, so callers can be given a fake.
type Func func(image.Image) (Result, error)

// MinConfidence is the lowest score acted on. Tesseract's own guidance
// treats scores near 1 as noise; every correct answer measured on real
// pages was above 2.
const MinConfidence = 1.5

// maxSide bounds the longer side of the copy handed to tesseract, about
// 150 dpi for a letter page. That is enough to read the text and keeps a
// Raspberry Pi's time per page down.
const maxSide = 1700

// osdTimeout bounds one page's tesseract run. A page takes about a second
// on a desktop and several on a Raspberry Pi; this is for a run that has
// hung, which would otherwise block the scan's post-processing (and keep
// the scan "running", refusing new ones) for good.
var osdTimeout = 90 * time.Second

// seams for tests.
var (
	lookPath = exec.LookPath
	runOSD   = func(path string) ([]byte, error) {
		ctx, cancel := context.WithTimeout(context.Background(), osdTimeout)
		defer cancel()
		cmd := exec.CommandContext(ctx, "tesseract", path, "stdout", "--psm", "0", "-l", "osd")
		cmd.WaitDelay = 2 * time.Second // don't wait on a killed run's open pipes
		return cmd.Output()
	}
	listLangs = func() ([]byte, error) {
		return exec.Command("tesseract", "--list-langs").Output()
	}
	mkdirTemp = os.MkdirTemp
)

// Available reports whether Detect can work: tesseract is installed and
// has the osd language data.
func Available() bool {
	if _, err := lookPath("tesseract"); err != nil {
		return false
	}
	out, err := listLangs()
	if err != nil {
		return false
	}
	for _, line := range strings.Fields(string(out)) {
		if line == "osd" {
			return true
		}
	}
	return false
}

// Detect returns which clockwise turn makes img's text upright.
func Detect(img image.Image) (Result, error) {
	if _, err := lookPath("tesseract"); err != nil {
		return Result{}, ErrToolNotAvailable
	}
	small := img
	if b := img.Bounds(); max(b.Dx(), b.Dy()) > maxSide {
		if b.Dx() >= b.Dy() {
			small = imaging.Resize(img, maxSide, 0, imaging.Box)
		} else {
			small = imaging.Resize(img, 0, maxSide, imaging.Box)
		}
	}
	dir, err := mkdirTemp("", "doxie-orient-*")
	if err != nil {
		return Result{}, fmt.Errorf("orient: temp dir: %w", err)
	}
	defer os.RemoveAll(dir)

	path := filepath.Join(dir, "page.png")
	var buf bytes.Buffer
	if err := png.Encode(&buf, imaging.Grayscale(small)); err != nil {
		return Result{}, fmt.Errorf("orient: encode: %w", err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		return Result{}, fmt.Errorf("orient: write: %w", err)
	}

	out, err := runOSD(path)
	if err != nil {
		// tesseract exits non-zero ("Too few characters") when there is
		// nothing to read: not an error worth surfacing.
		return Result{}, ErrUndetected
	}
	res, ok := parse(string(out))
	if !ok || res.Confidence < MinConfidence {
		return Result{}, ErrUndetected
	}
	return res, nil
}

// parse reads tesseract's "Rotate:" and "Orientation confidence:" lines.
func parse(out string) (Result, bool) {
	var res Result
	var haveRotate, haveConf bool
	for _, line := range strings.Split(out, "\n") {
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		val = strings.TrimSpace(val)
		switch strings.TrimSpace(key) {
		case "Rotate":
			n, err := strconv.Atoi(val)
			if err != nil || n%90 != 0 || n < 0 || n > 270 {
				return Result{}, false
			}
			res.Rotate, haveRotate = n, true
		case "Orientation confidence":
			f, err := strconv.ParseFloat(val, 64)
			if err != nil {
				return Result{}, false
			}
			res.Confidence, haveConf = f, true
		}
	}
	return res, haveRotate && haveConf
}
