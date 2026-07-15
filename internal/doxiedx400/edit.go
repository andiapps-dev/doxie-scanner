package doxiedx400

import (
	"fmt"
	"image"

	"github.com/disintegration/imaging"
)

// Rotate returns a copy of img rotated clockwise by degrees, which must
// be 90, 180, or 270. imaging's Rotate90/180/270 rotate counter-clockwise,
// so the mapping below inverts that to give callers (and the HTTP API) an
// intuitive clockwise "rotate this page by N degrees" contract.
func Rotate(img image.Image, degrees int) (*image.NRGBA, error) {
	switch degrees {
	case 90:
		return imaging.Rotate270(img), nil
	case 180:
		return imaging.Rotate180(img), nil
	case 270:
		return imaging.Rotate90(img), nil
	default:
		return nil, fmt.Errorf("doxiedx400: unsupported rotation %d degrees (must be 90, 180, or 270)", degrees)
	}
}

// Crop returns a copy of img cropped to the given rectangle, clipped to
// img's own bounds so an out-of-range request can't panic or silently
// produce an empty image.
func Crop(img image.Image, rect image.Rectangle) (*image.NRGBA, error) {
	clamped := rect.Intersect(img.Bounds())
	if clamped.Empty() {
		return nil, fmt.Errorf("doxiedx400: crop rectangle %v does not intersect image bounds %v", rect, img.Bounds())
	}
	return imaging.Crop(img, clamped), nil
}
