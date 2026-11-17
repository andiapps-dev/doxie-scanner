package autocrop

import (
	"image"
	"image/color"
	"math"
	"testing"
)

var (
	backing = color.NRGBA{195, 198, 197, 255} // the scanner's gray, as measured on real scans
	paper   = color.NRGBA{236, 235, 232, 255}
	ink     = color.NRGBA{40, 40, 60, 255}
)

// scan draws a w x h scan of the scanner's backing with a sheet of sheetW x
// sheetH centered on it, turned clockwise by tilt degrees, with a few bars
// of "print" so the sheet is not blank. The scan starts with a short run of
// backing rows (the detector looks for the sheet below the scan's start).
func scan(w, h, sheetW, sheetH int, tilt float64) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	sin, cos := math.Sincos(tilt * math.Pi / 180)
	cx, cy := float64(w)/2, float64(h)/2
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dx, dy := float64(x)+0.5-cx, float64(y)+0.5-cy
			u, v := dx*cos+dy*sin, -dx*sin+dy*cos // position on the sheet
			c := backing
			if math.Abs(u) < float64(sheetW)/2 && math.Abs(v) < float64(sheetH)/2 {
				c = paper
				if int(v+float64(sheetH)/2)%60 < 8 && math.Abs(u) < float64(sheetW)/2-40 {
					c = ink
				}
			}
			img.SetNRGBA(x, y, c)
		}
	}
	return img
}

func TestProcess_CropsASmallSheetToItself(t *testing.T) {
	src := scan(1275, 940, 600, 900, 0)
	out, res, ok := Process(src)
	if !ok {
		t.Fatal("a 600-wide sheet on a 1275-wide scan should be cropped")
	}
	if b := out.Bounds(); b.Dx() < 590 || b.Dx() > 640 || b.Dy() < 880 {
		t.Errorf("cropped to %v, want about 600x900", b)
	}
	if res.FromWidth != 1275 || res.FromHeight != 940 {
		t.Errorf("result remembers %dx%d, want the scan's 1275x940", res.FromWidth, res.FromHeight)
	}
	if res.Straighten != 0 {
		t.Errorf("a level sheet was straightened by %.2f°", res.Straighten)
	}
	// What is left is the sheet: no backing grays in the corners.
	for _, p := range []image.Point{{3, 3}, {out.Bounds().Dx() - 4, 3}, {3, out.Bounds().Dy() - 4}} {
		if c := out.NRGBAAt(p.X, p.Y); c.R < 215 {
			t.Errorf("backing left at %v: %+v", p, c)
		}
	}
}

func TestProcess_StraightensATiltedSheet(t *testing.T) {
	src := scan(1275, 1000, 600, 900, 2) // turned 2° clockwise
	_, res, ok := Process(src)
	if !ok {
		t.Fatal("a tilted small sheet should be cropped")
	}
	if math.Abs(res.Straighten) < MinStraighten || math.Abs(res.Straighten-2) > 0.6 && math.Abs(res.Straighten+2) > 0.6 {
		t.Errorf("straightened by %.2f°, want about 2° either way", res.Straighten)
	}
}

func TestProcess_LeavesAHairOfTiltAlone(t *testing.T) {
	src := scan(1275, 940, 600, 900, 0.2)
	_, res, ok := Process(src)
	if !ok {
		t.Fatal("should still be cropped")
	}
	if res.Straighten != 0 {
		t.Errorf("a %.1f° tilt was turned (%.2f°): resampling for a hair is not worth it", 0.2, res.Straighten)
	}
}

func TestProcess_LeavesPagesThatFillTheScanAlone(t *testing.T) {
	for name, src := range map[string]*image.NRGBA{
		"a page filling the scan":        scan(1275, 1650, 1275, 1650, 0),
		"a page with a slim backing rim": scan(1275, 1650, 1235, 1610, 0),
		"a blank scanner":                scan(1275, 940, 0, 0, 0),
	} {
		if out, _, ok := Process(src); ok {
			t.Errorf("%s was changed to %v", name, out.Bounds())
		}
	}
}

func TestProcess_NeedsEnoughBackingToAct(t *testing.T) {
	// A sheet taking 85% of the scan's width leaves 15% backing: below the
	// bar, so it is left as scanned even though the detector sees edges.
	src := scan(1275, 940, 1085, 940, 0)
	if out, _, ok := Process(src); ok {
		t.Errorf("only 15%% backing but cropped to %v", out.Bounds())
	}
}
