// Package autocrop trims a scan of a small sheet (a photo, a card, a
// receipt) to the sheet, straightening it if it fed in crooked. The DX400
// always reads its full 8.5 inch width, so such a sheet comes out as a
// small picture floating in the scanner's gray backing.
//
// Finding the sheet is internal/pageimage, the detector from the
// jasonlearst/doxie-scanner fork. This package adds the rule for when to
// act on what it finds, which came from running it on real scans:
//
//   - On photos and cards the detector found the sheet reliably, and the
//     backing it removed was 26% to 56% of the scan.
//   - On ordinary letter pages, which fill the scan, it found only the
//     thin dark fringe at the edges (1% to 11%), and on one page it was
//     confidently wrong: a made-up 3.4° tilt and a crop through the page's
//     footer.
//
// So it acts only when a large part of the scan is backing, and leaves
// every page that fills the scan exactly as it was.
package autocrop

import (
	"image"
	"math"

	"github.com/andiapps-dev/doxie-scanner/internal/pageimage"
)

const (
	// MinRemoved is the share of the scan that must be backing before
	// anything is changed. The gap in real scans was 11% (documents) to
	// 26% (photos); this sits inside it, nearer the documents.
	MinRemoved = 0.20
	// MinStraighten is the smallest tilt worth correcting. Turning an
	// image resamples it, so a sheet that is only a hair off is left.
	MinStraighten = 0.5
)

// Result is what was done to a page, so it can be shown and undone.
type Result struct {
	Crop       pageimage.Crop // the sheet, in the straightened frame
	Straighten float64        // degrees counter-clockwise; 0 when left unturned
	// FromWidth and FromHeight are the scan's size before the crop.
	FromWidth, FromHeight int
}

// Process returns img trimmed to its sheet, or ok=false when the scan
// should be left alone: no sheet found, or not enough backing to be sure.
func Process(img image.Image) (out *image.NRGBA, res Result, ok bool) {
	paper, found := pageimage.DetectPaper(img)
	if !found {
		return nil, Result{}, false
	}
	b := img.Bounds()
	kept := float64(paper.Crop.W*paper.Crop.H) / float64(b.Dx()*b.Dy())
	if 1-kept < MinRemoved {
		return nil, Result{}, false
	}
	edits := pageimage.Edits{Crop: &paper.Crop}
	res = Result{Crop: paper.Crop, FromWidth: b.Dx(), FromHeight: b.Dy()}
	if math.Abs(paper.Straighten) >= MinStraighten {
		edits.Straighten = paper.Straighten
		res.Straighten = paper.Straighten
	}
	// With a crop set, Render always ends in imaging.Crop, an *image.NRGBA.
	return pageimage.Render(img, edits).(*image.NRGBA), res, true
}
