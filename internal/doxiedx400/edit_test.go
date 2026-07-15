package doxiedx400

import (
	"image"
	"image/color"
	"testing"
)

// cornerImage builds a 2x3 (WxH) image with distinct colors at three
// corners, used to verify rotation direction empirically rather than by
// reasoning about the underlying library's own CW/CCW labeling.
func cornerImage() *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, 2, 3))
	red := color.NRGBA{R: 255, A: 255}
	green := color.NRGBA{G: 255, A: 255}
	blue := color.NRGBA{B: 255, A: 255}
	img.Set(0, 0, red)   // top-left
	img.Set(1, 0, green) // top-right
	img.Set(0, 2, blue)  // bottom-left
	return img
}

func at(img image.Image, x, y int) color.NRGBA {
	r, g, b, a := img.At(x, y).RGBA()
	return color.NRGBA{R: uint8(r >> 8), G: uint8(g >> 8), B: uint8(b >> 8), A: uint8(a >> 8)}
}

func TestRotate_90ClockwiseMovesTopLeftToTopRight(t *testing.T) {
	img := cornerImage()
	rotated, err := Rotate(img, 90)
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if rotated.Bounds().Dx() != 3 || rotated.Bounds().Dy() != 2 {
		t.Fatalf("expected dimensions to swap to 3x2, got %v", rotated.Bounds())
	}
	// Rotating 90 degrees clockwise: original top-left -> new top-right.
	got := at(rotated, rotated.Bounds().Dx()-1, 0)
	want := color.NRGBA{R: 255, A: 255} // red
	if got != want {
		t.Errorf("top-right corner after 90 deg CW: got %v, want %v", got, want)
	}
}

func TestRotate_180FlipsBothAxes(t *testing.T) {
	img := cornerImage()
	rotated, err := Rotate(img, 180)
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if rotated.Bounds().Dx() != 2 || rotated.Bounds().Dy() != 3 {
		t.Fatalf("expected dimensions to stay 2x3, got %v", rotated.Bounds())
	}
	// Original top-left (red) should now be at bottom-right.
	got := at(rotated, rotated.Bounds().Dx()-1, rotated.Bounds().Dy()-1)
	want := color.NRGBA{R: 255, A: 255}
	if got != want {
		t.Errorf("bottom-right corner after 180: got %v, want %v", got, want)
	}
}

func TestRotate_270ClockwiseMovesTopLeftToBottomLeft(t *testing.T) {
	img := cornerImage()
	rotated, err := Rotate(img, 270)
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if rotated.Bounds().Dx() != 3 || rotated.Bounds().Dy() != 2 {
		t.Fatalf("expected dimensions to swap to 3x2, got %v", rotated.Bounds())
	}
	// Rotating 270 degrees clockwise (= 90 CCW): original top-left -> new bottom-left.
	got := at(rotated, 0, rotated.Bounds().Dy()-1)
	want := color.NRGBA{R: 255, A: 255}
	if got != want {
		t.Errorf("bottom-left corner after 270 deg CW: got %v, want %v", got, want)
	}
}

func TestRotate_UnsupportedDegreesErrors(t *testing.T) {
	img := cornerImage()
	if _, err := Rotate(img, 45); err == nil {
		t.Fatal("expected an error for an unsupported rotation")
	}
}

func TestCrop_WithinBounds(t *testing.T) {
	img := cornerImage()
	cropped, err := Crop(img, image.Rect(0, 0, 1, 1))
	if err != nil {
		t.Fatalf("Crop: %v", err)
	}
	if cropped.Bounds().Dx() != 1 || cropped.Bounds().Dy() != 1 {
		t.Fatalf("got %v", cropped.Bounds())
	}
	got := at(cropped, 0, 0)
	want := color.NRGBA{R: 255, A: 255}
	if got != want {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestCrop_ClampsToImageBounds(t *testing.T) {
	img := cornerImage()
	// Requests a region that extends well beyond the image; should clamp
	// rather than panic.
	cropped, err := Crop(img, image.Rect(-5, -5, 100, 100))
	if err != nil {
		t.Fatalf("Crop: %v", err)
	}
	if cropped.Bounds().Dx() != 2 || cropped.Bounds().Dy() != 3 {
		t.Errorf("expected clamp to full image bounds 2x3, got %v", cropped.Bounds())
	}
}

func TestCrop_EntirelyOutsideBoundsErrors(t *testing.T) {
	img := cornerImage()
	if _, err := Crop(img, image.Rect(100, 100, 200, 200)); err == nil {
		t.Fatal("expected an error for a crop rectangle outside the image")
	}
}
