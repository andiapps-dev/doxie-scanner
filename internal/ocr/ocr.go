// Package ocr extracts text from a scanned page image by shelling out
// to two external tools: unpaper (deskew/cleanup) and tesseract (OCR).
// Neither has a usable pure-Go equivalent, so this package isolates them
// as subprocesses over real files — the same "isolate the one seam that
// can't be pure Go" approach internal/scsiusb takes for USB hardware,
// except the seam here is a subprocess boundary, not a device.
package ocr

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// ErrToolNotAvailable is returned when unpaper or tesseract isn't
// installed / isn't on $PATH.
var ErrToolNotAvailable = errors.New("ocr: required external tool not found (unpaper and tesseract must both be installed)")

// DPI must match the resolution pages are actually scanned/stored at
// (see pdfexport.ScanDPI) — unpaper uses this to interpret its
// measurement options and to tune its deskew/cleanup heuristics
// correctly for the real physical page size.
const DPI = 300

// mkdirTemp is a package-level indirection purely so tests can exercise
// the (effectively unreachable in practice — a broken $TMPDIR) failure
// path without needing to actually break the host's filesystem (see the
// equivalent pattern for crypto/rand in internal/storage/metadata.go).
var mkdirTemp = os.MkdirTemp

// ExtractText runs the deskew-then-OCR pipeline against the PNG file at
// imagePath (a real on-disk stored page, e.g. from
// storage.Store.PageFilePath) and returns the recognized text. lang is a
// tesseract language code (e.g. "eng"); the corresponding
// tesseract-ocr-data-<lang> (Alpine) / tesseract-ocr-<lang> (Debian)
// package must be installed for anything other than "eng".
//
// The source PNG is only ever read, never modified — deskewing happens
// on a temporary copy used solely for this OCR pass, exactly like Export
// derives a representation without mutating the stored page.
func ExtractText(imagePath, lang string) (string, error) {
	pngData, err := os.ReadFile(imagePath)
	if err != nil {
		return "", fmt.Errorf("ocr: read %q: %w", imagePath, err)
	}
	img, err := png.Decode(bytes.NewReader(pngData))
	if err != nil {
		return "", fmt.Errorf("ocr: decode %q: %w", imagePath, err)
	}

	tmpDir, err := mkdirTemp("", "doxie-ocr-*")
	if err != nil {
		return "", fmt.Errorf("ocr: create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	// unpaper accepts PNM (.pbm/.pgm/.ppm) input only — not PNG, despite
	// linking against libav/FFmpeg internally for some other purpose.
	// Tesseract, on the other hand, reads PNM natively via leptonica, so
	// only one conversion (PNG -> PPM, in) is needed; unpaper's own PPM
	// output feeds directly into tesseract with no second conversion.
	inputPPM := filepath.Join(tmpDir, "input.ppm")
	if err := writePPM(inputPPM, img); err != nil {
		return "", fmt.Errorf("ocr: write input ppm: %w", err)
	}

	correctedPPM := filepath.Join(tmpDir, "corrected.ppm")
	if err := runUnpaper(inputPPM, correctedPPM); err != nil {
		return "", err
	}

	return runTesseract(correctedPPM, lang)
}

// runUnpaper deskews inputPath (a PPM file), writing the result to
// outputPath. Deskewing is unpaper's default behavior (it auto-detects
// rotation up to +/-5 degrees by scanning the page's outer edges) — no
// special flag is needed to enable it, only to avoid disabling it.
//
// Every other cleanup pass unpaper runs by default (blackfilter,
// noisefilter, blurfilter, grayfilter, border-scan/align/wipe) is aimed
// at photocopied book scans — irrelevant for a single flatbed page
// headed straight to OCR, and expensive: disabling them cut unpaper's
// time on a real ~8-megapixel scan from ~7.2s to ~2.1s (measured), with
// no change in deskew accuracy.
func runUnpaper(inputPath, outputPath string) error {
	cmd := exec.Command("unpaper",
		"--dpi", strconv.Itoa(DPI), "-t", "ppm",
		"--no-blackfilter", "--no-noisefilter", "--no-blurfilter", "--no-grayfilter",
		"--no-border-scan", "--no-border-align", "--no-wipe", "--no-border",
		inputPath, outputPath)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return ErrToolNotAvailable
		}
		return fmt.Errorf("ocr: unpaper: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// runTesseract OCRs inputPath (a PPM file) and returns the recognized
// text. "stdout" as the output argument tells tesseract to write
// directly to standard output instead of a <name>.txt file.
func runTesseract(inputPath, lang string) (string, error) {
	cmd := exec.Command("tesseract", inputPath, "stdout", "-l", lang)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return "", ErrToolNotAvailable
		}
		return "", fmt.Errorf("ocr: tesseract: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

// writePPM encodes img as a binary (P6) PPM file — the raw format
// unpaper requires as input.
func writePPM(path string, img image.Image) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return encodePPM(f, img)
}

// encodePPM is writePPM's actual encoding logic, split out from the
// file-handling above purely so tests can exercise a mid-write failure
// (header write vs. row write) via a fake io.Writer, without needing to
// engineer a real filesystem failure partway through a file.
func encodePPM(w io.Writer, img image.Image) error {
	bounds := img.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if _, err := fmt.Fprintf(w, "P6\n%d %d\n255\n", width, height); err != nil {
		return err
	}
	row := make([]byte, 0, width*3)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		row = row[:0]
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, g, b, _ := img.At(x, y).RGBA()
			row = append(row, byte(r>>8), byte(g>>8), byte(b>>8))
		}
		if _, err := w.Write(row); err != nil {
			return err
		}
	}
	return nil
}
