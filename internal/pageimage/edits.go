package pageimage

import (
	"image"
	"image/color"

	"github.com/disintegration/imaging"
)

// This package is the sheet detector from github.com/jasonlearst/doxie-scanner
// (MIT), which forked this project: paper.go is theirs as written, and
// edits.go is cut down to the crop and straighten parts it uses. The fork
// applies these as non-destructive edits; this app applies them once, to
// the stored page, keeping the original (see internal/autocrop).
//
// DetectSheet, which reads both sides of a duplex sheet together and lets
// each side fill in an edge the other found, is not used yet: auto-crop
// looks at each page on its own, so a photo where only one edge shows is
// trimmed on that side only. Wiring it in is the way to fix that.

// Crop is a rectangle in a page's pixel space.
type Crop struct {
	X int `json:"x"`
	Y int `json:"y"`
	W int `json:"w"`
	H int `json:"h"`
}

func (c Crop) rect() image.Rectangle { return image.Rect(c.X, c.Y, c.X+c.W, c.Y+c.H) }

// Edits are what Render applies to a page.
type Edits struct {
	Straighten float64 `json:"straighten,omitempty"` // degrees counter-clockwise, applied first
	Rotation   int     `json:"rotation,omitempty"`   // clockwise: 0, 90, 180, 270
	Crop       *Crop   `json:"crop,omitempty"`       // in the straightened, rotated frame; nil = whole page
}

func rotatedSize(w, h, rotation int) (int, int) {
	if rotation == 90 || rotation == 270 {
		return h, w
	}
	return w, h
}

// rotateCrop maps a crop between the unrotated frame (w x h) and the frame
// rotated clockwise by rotation; inverse maps rotated back to unrotated.
func rotateCrop(c Crop, w, h, rotation int, inverse bool) Crop {
	if inverse {
		rw, rh := rotatedSize(w, h, rotation)
		return rotateCrop(c, rw, rh, (360-rotation)%360, false)
	}
	x0, y0, x1, y1 := c.X, c.Y, c.X+c.W, c.Y+c.H
	switch rotation {
	case 90:
		x0, y0, x1, y1 = h-y1, x0, h-y0, x1
	case 180:
		x0, y0, x1, y1 = w-x1, h-y1, w-x0, h-y0
	case 270:
		x0, y0, x1, y1 = y0, w-x1, y1, w-x0
	}
	return Crop{X: x0, Y: y0, W: x1 - x0, H: y1 - y0}
}

// Render applies edits: straighten, crop (mapped back to the unrotated
// frame, so the rotation works on fewer pixels), then rotate.
func Render(img image.Image, e Edits) image.Image {
	out := img
	if e.Straighten != 0 {
		out = straighten(out, e.Straighten)
	}
	if c := e.Crop; c != nil {
		b := img.Bounds()
		o := rotateCrop(*c, b.Dx(), b.Dy(), e.Rotation, true)
		out = imaging.Crop(out, o.rect().Add(b.Min))
	}
	switch e.Rotation {
	case 90:
		out = imaging.Rotate270(out)
	case 180:
		out = imaging.Rotate180(out)
	case 270:
		out = imaging.Rotate90(out)
	}
	return out
}

// straighten turns img counter-clockwise by degrees about its center and
// keeps its size, so crops stay in the same frame. The corners it uncovers
// are white, like paper.
func straighten(img image.Image, degrees float64) image.Image {
	b := img.Bounds()
	turned := imaging.Rotate(img, degrees, color.White)
	tb := turned.Bounds()
	off := image.Pt((tb.Dx()-b.Dx())/2, (tb.Dy()-b.Dy())/2)
	return imaging.Crop(turned, image.Rectangle{Min: off, Max: off.Add(b.Size())})
}
