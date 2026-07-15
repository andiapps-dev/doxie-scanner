package doxiedx400

import "image"

// blankDarkThreshold and blankInkFractionThreshold calibrate the
// blank-page heuristic used to auto-drop an unprinted duplex back side.
// Real measurements (documented in this project's history) found genuine
// blank backs measure ink_fraction ~0.003-0.012 (front-side bleed-through
// and scanner shading alone), while well-calibrated real content measures
// ~0.05-0.09 — these thresholds sit with margin in that gap. A genuinely
// printed but very faint back side could still land in the ambiguous
// range and be misclassified as blank; that's a known, accepted
// trade-off, not a bug.
const (
	blankDarkThreshold        = 180
	blankInkFractionThreshold = 0.02
)

// rawToImage reshapes packed 3-bytes-per-pixel RGB scanner data (no
// compression, no calibration math needed — the scanner's own gamma/
// matrix downloads already handled that on-device) into an image.NRGBA.
// NRGBA (rather than a custom 3-byte-per-pixel image.Image) is chosen so
// the standard library's PNG/JPEG encoders get their optimized fast
// paths; the ~33% extra memory for the synthesized alpha channel is a
// fine trade for a low-throughput scanning utility. Trailing bytes that
// don't complete a whole scanline are silently discarded, matching the
// reference implementation.
func rawToImage(raw []byte) *image.NRGBA {
	nLines := len(raw) / lineWidthBytes
	img := image.NewNRGBA(image.Rect(0, 0, imageWidthPx, nLines))
	for y := 0; y < nLines; y++ {
		srcRow := raw[y*lineWidthBytes : (y+1)*lineWidthBytes]
		dstOff := y * img.Stride
		for x := 0; x < imageWidthPx; x++ {
			s := x * channels
			d := dstOff + x*4
			img.Pix[d+0] = srcRow[s+0]
			img.Pix[d+1] = srcRow[s+1]
			img.Pix[d+2] = srcRow[s+2]
			img.Pix[d+3] = 0xff
		}
	}
	return img
}

// splitDuplexChunks separates alternating front/rear image-data stripes:
// even-indexed chunks are the front side, odd-indexed are the rear side.
// See protocol.go's readChunkBytes doc comment for why a whole READ chunk
// coincides exactly with one duplex stripe.
func splitDuplexChunks(chunks [][]byte) (front, rear []byte) {
	var frontLen, rearLen int
	for i, c := range chunks {
		if i%2 == 0 {
			frontLen += len(c)
		} else {
			rearLen += len(c)
		}
	}
	front = make([]byte, 0, frontLen)
	rear = make([]byte, 0, rearLen)
	for i, c := range chunks {
		if i%2 == 0 {
			front = append(front, c...)
		} else {
			rear = append(rear, c...)
		}
	}
	return front, rear
}

// isBlank reports whether raw scanner data looks like an unprinted page:
// the fraction of pixels darker (by mean of R,G,B) than
// blankDarkThreshold is below blankInkFractionThreshold.
func isBlank(raw []byte) bool {
	nLines := len(raw) / lineWidthBytes
	if nLines == 0 {
		return true
	}
	totalPixels := nLines * imageWidthPx
	usable := raw[:nLines*lineWidthBytes]

	dark := 0
	for i := 0; i < totalPixels; i++ {
		o := i * channels
		gray := (float64(usable[o]) + float64(usable[o+1]) + float64(usable[o+2])) / 3.0
		if gray < blankDarkThreshold {
			dark++
		}
	}
	inkFraction := float64(dark) / float64(totalPixels)
	return inkFraction < blankInkFractionThreshold
}
