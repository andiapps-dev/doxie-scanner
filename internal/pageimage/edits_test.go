package pageimage

import (
	"image"
	"image/color"
	"image/draw"
	"math"
	"testing"
)

// tilted draws a scene turned clockwise by degrees about the image center:
// paint gets each pixel's position in the untilted scene, relative to the
// center. (From the jasonlearst/doxie-scanner fork's tests.)
func tilted(w, h int, degrees float64, paint func(x, y float64) color.Color) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	sin, cos := math.Sincos(degrees * math.Pi / 180)
	cx, cy := float64(w)/2, float64(h)/2
	for py := 0; py < h; py++ {
		for px := 0; px < w; px++ {
			dx, dy := float64(px)+0.5-cx, float64(py)+0.5-cy
			img.Set(px, py, paint(dx*cos+dy*sin, -dx*sin+dy*cos))
		}
	}
	return img
}

// barColumn is the mean column of the dark pixels in row y.
func barColumn(img image.Image, y int) float64 {
	b := img.Bounds()
	sum, n := 0.0, 0
	for x := b.Min.X; x < b.Max.X; x++ {
		if r, _, _, _ := img.At(x, y).RGBA(); r < 0x8000 {
			sum += float64(x)
			n++
		}
	}
	return sum / float64(n)
}

// solid is a w x h image of one color. (From the fork's tests.)
func solid(w, h int, c color.Color) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), &image.Uniform{c}, image.Point{}, draw.Src)
	return img
}

// marked is a 60x40 page with a red pixel at (5, 3), to follow through edits.
func marked() *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, 60, 40))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = 255, 255, 255, 255
	}
	img.Set(5, 3, color.NRGBA{255, 0, 0, 255})
	return img
}

func TestRender_CropsInThePageFrame(t *testing.T) {
	out := Render(marked(), Edits{Crop: &Crop{X: 4, Y: 2, W: 20, H: 10}})
	if b := out.Bounds(); b.Dx() != 20 || b.Dy() != 10 {
		t.Fatalf("size = %v", b)
	}
	if r, g, _, _ := out.At(1, 1).RGBA(); r>>8 != 255 || g>>8 != 0 {
		t.Error("the marked pixel should sit at (1,1) after the crop")
	}
}

func TestRender_RotationMovesCropAndPixels(t *testing.T) {
	// The crop is given in the rotated frame, so a 90° page of 60x40 is 40x60.
	out := Render(marked(), Edits{Rotation: 90, Crop: &Crop{X: 0, Y: 0, W: 40, H: 30}})
	if b := out.Bounds(); b.Dx() != 40 || b.Dy() != 30 {
		t.Fatalf("size = %v", b)
	}
	for _, rot := range []int{180, 270} {
		out := Render(marked(), Edits{Rotation: rot})
		w, h := rotatedSize(60, 40, rot)
		if b := out.Bounds(); b.Dx() != w || b.Dy() != h {
			t.Errorf("rotation %d: size = %v, want %dx%d", rot, b, w, h)
		}
	}
}

func TestRotateCrop_RoundTrips(t *testing.T) {
	c := Crop{X: 7, Y: 3, W: 20, H: 11}
	for _, rot := range []int{0, 90, 180, 270} {
		there := rotateCrop(c, 60, 40, rot, false)
		if back := rotateCrop(there, 60, 40, rot, true); back != c {
			t.Errorf("rotation %d: %+v came back as %+v", rot, c, back)
		}
	}
}

func TestRender_StraightenKeepsTheSizeAndTurnsCounterClockwise(t *testing.T) {
	// A vertical dark bar, tilted 3° clockwise, straightens back to vertical.
	scene := tilted(400, 300, 3, func(x, y float64) color.Color {
		if math.Abs(x) < 6 {
			return color.Black
		}
		return color.White
	})
	if top, bottom := barColumn(scene, 20), barColumn(scene, 280); math.Abs(top-bottom) < 5 {
		t.Fatalf("the fixture is not tilted: %.1f vs %.1f", top, bottom)
	}
	out := Render(scene, Edits{Straighten: 3})
	if b := out.Bounds(); b.Dx() != 400 || b.Dy() != 300 {
		t.Fatalf("size = %v", b)
	}
	if top, bottom := barColumn(out, 60), barColumn(out, 240); math.Abs(top-bottom) > 1.5 {
		t.Errorf("still tilted after straightening: %.1f vs %.1f", top, bottom)
	}
}
