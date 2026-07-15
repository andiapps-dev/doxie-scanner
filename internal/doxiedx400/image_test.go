package doxiedx400

import (
	"bytes"
	"testing"
)

func TestRawToImage_DimensionsAndPixels(t *testing.T) {
	// Two scanlines, each a solid distinguishable color.
	line0 := bytes.Repeat([]byte{0x10, 0x20, 0x30}, imageWidthPx)
	line1 := bytes.Repeat([]byte{0x40, 0x50, 0x60}, imageWidthPx)
	raw := append(append([]byte{}, line0...), line1...)

	img := rawToImage(raw)

	if img.Bounds().Dx() != imageWidthPx || img.Bounds().Dy() != 2 {
		t.Fatalf("got %dx%d, want %dx2", img.Bounds().Dx(), img.Bounds().Dy(), imageWidthPx)
	}

	r, g, b, a := img.At(0, 0).RGBA()
	if r>>8 != 0x10 || g>>8 != 0x20 || b>>8 != 0x30 || a>>8 != 0xff {
		t.Errorf("pixel (0,0): got r=%d g=%d b=%d a=%d", r>>8, g>>8, b>>8, a>>8)
	}
	r, g, b, a = img.At(0, 1).RGBA()
	if r>>8 != 0x40 || g>>8 != 0x50 || b>>8 != 0x60 || a>>8 != 0xff {
		t.Errorf("pixel (0,1): got r=%d g=%d b=%d a=%d", r>>8, g>>8, b>>8, a>>8)
	}
}

func TestRawToImage_TruncatesPartialTrailingLine(t *testing.T) {
	line0 := bytes.Repeat([]byte{0xAA, 0xBB, 0xCC}, imageWidthPx)
	partial := make([]byte, lineWidthBytes/2) // half a scanline: should be dropped entirely
	raw := append(append([]byte{}, line0...), partial...)

	img := rawToImage(raw)
	if img.Bounds().Dy() != 1 {
		t.Errorf("expected the partial trailing line to be discarded, got height %d", img.Bounds().Dy())
	}
}

func TestRawToImage_EmptyInput(t *testing.T) {
	img := rawToImage(nil)
	if img.Bounds().Dy() != 0 {
		t.Errorf("expected 0 lines for empty input, got %d", img.Bounds().Dy())
	}
}

func TestSplitDuplexChunks(t *testing.T) {
	chunks := [][]byte{
		{0x01}, // front
		{0x02}, // rear
		{0x03}, // front
		{0x04}, // rear
		{0x05}, // front (odd count: no matching rear)
	}
	front, rear := splitDuplexChunks(chunks)
	if !bytes.Equal(front, []byte{0x01, 0x03, 0x05}) {
		t.Errorf("front: got % x", front)
	}
	if !bytes.Equal(rear, []byte{0x02, 0x04}) {
		t.Errorf("rear: got % x", rear)
	}
}

func TestSplitDuplexChunks_Empty(t *testing.T) {
	front, rear := splitDuplexChunks(nil)
	if len(front) != 0 || len(rear) != 0 {
		t.Errorf("expected empty front/rear, got %v %v", front, rear)
	}
}

func TestIsBlank_EmptyDataIsBlank(t *testing.T) {
	if !isBlank(nil) {
		t.Error("expected empty data to be classified as blank")
	}
}

func TestIsBlank_SolidWhiteIsBlank(t *testing.T) {
	raw := bytes.Repeat([]byte{250, 248, 252}, imageWidthPx*100)
	if !isBlank(raw) {
		t.Error("expected solid near-white page to be classified as blank")
	}
}

func TestIsBlank_SolidBlackIsNotBlank(t *testing.T) {
	raw := bytes.Repeat([]byte{0, 0, 0}, imageWidthPx*100)
	if isBlank(raw) {
		t.Error("expected solid black page to be classified as not blank")
	}
}

func TestIsBlank_RealisticMixIsNotBlank(t *testing.T) {
	// ~10% of pixels dark, comfortably above the 2% threshold.
	nLines := 100
	raw := make([]byte, nLines*lineWidthBytes)
	for i := range raw {
		raw[i] = 250
	}
	darkPixels := (nLines * imageWidthPx) / 10
	for p := 0; p < darkPixels; p++ {
		o := p * channels
		raw[o], raw[o+1], raw[o+2] = 10, 10, 10
	}
	if isBlank(raw) {
		t.Error("expected ~10% dark coverage to be classified as not blank")
	}
}
