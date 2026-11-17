package pageimage

import (
	"image"
	"math"
	"slices"

	"github.com/disintegration/imaging"
)

// Paper is where the sheet lies in a scan. The DX400 always reads its full
// width, so a narrower sheet lies between strips of its gray backing, and
// one that fed in crooked lies at an angle.
type Paper struct {
	Straighten float64 `json:"straighten,omitempty"` // as in Edits
	Crop       Crop    `json:"crop"`                 // in the straightened, unrotated frame
}

// Apply returns e straightened and cropped to the paper, for a page whose
// decoded size is width x height.
func (p Paper) Apply(e Edits, width, height int) Edits {
	e.Straighten = p.Straighten
	c := rotateCrop(p.Crop, width, height, e.Rotation, false)
	e.Crop = &c
	return e
}

const (
	paperScanWidth    = 850  // width of the copy the sheet is looked for in
	backingChroma     = 24   // the backing is neutral gray...
	backingTolerance  = 22   // ...within this of its measured brightness
	defaultBacking    = 176  // what it measures when no strip shows
	edgeStrip         = 4    // pixels at the scan's edge, about 1 mm, that may be unlike the backing inside them
	edgeRun           = 3    // pixels in a row unlike the backing that mark the sheet's edge
	edgeSearch        = 0.45 // how far in from each side to look
	minEdgeShare      = 0.15 // share of rows or columns that must show an edge to trust it
	edgeInlier        = 2.0  // pixels off the fitted edge line still on it
	minStraighten     = 0.15 // degrees; smaller tilts are left alone
	maxAutoStraighten = 5.0  // degrees; larger ones aren't a feeding error
	minPaperShare     = 0.2  // smallest sheet, as a share of the scan's width and height
)

// The sheet's edges, in the order detection keeps them.
const (
	edgeLeft = iota
	edgeRight
	edgeTop
	edgeBottom
)

// DetectPaper finds the sheet in a scan by where the scanner's backing
// ends, and reports how far to straighten it and where to crop. It
// reports false when there is nothing to change: no backing shows, or the
// sheet already fills the scan.
func DetectPaper(img image.Image) (Paper, bool) {
	return detect(img).ownPaper()
}

// DetectSheet finds the sheet in the front and back of a two-sided scan,
// each as DetectPaper does, and returns nil for a side with nothing to
// change. A side that found only one of its left and right edges, where
// the other side found both, then takes the other side's width, anchored
// on the edge it found, and likewise for height. If such a side came out
// unstraightened, it takes the other side's tilt reversed, as the back is
// the front's mirror image. Nothing a side found itself is overridden.
// back is nil for a sheet scanned on one side.
func DetectSheet(front, back image.Image) (frontPaper, backPaper *Paper) {
	f := detect(front)
	if back == nil {
		return f.beside(nil), nil
	}
	b := detect(back)
	return f.beside(b), b.beside(f)
}

// detection is what a side of a scan shows of the sheet: the edges where
// the backing showed beside it, and how far it is tilted.
type detection struct {
	g             *grid
	top, bottom   int          // grid rows the sheet is looked for between
	edges         [4]*edgeLine // by edgeLeft..edgeBottom; ok where found
	tilt          float64      // degrees, 0 when it's too small or too large to straighten
	width, height int          // the scan's size
}

// detect returns nil for a scan too small to find a sheet in.
func detect(img image.Image) *detection {
	b := img.Bounds()
	g := newGrid(img)
	top, bottom := g.startLine(), g.h
	if bottom-top < 10 || g.w < 10 {
		return nil
	}
	d := &detection{g: g, top: top, bottom: bottom, edges: g.edges(top, bottom), width: b.Dx(), height: b.Dy()}

	var tilts []weighted
	for i, e := range d.edges {
		enough := minEdgeShare * float64(g.w)
		if vertical(i) {
			enough = minEdgeShare * float64(bottom-top)
		}
		if !e.fit(enough) {
			continue
		}
		deg := math.Atan(e.slope) * 180 / math.Pi
		if vertical(i) {
			deg = -deg
		}
		tilts = append(tilts, weighted{deg, float64(len(e.inliers))})
	}
	d.tilt = consensus(tilts)
	if math.Abs(d.tilt) < minStraighten || math.Abs(d.tilt) > maxAutoStraighten {
		d.tilt = 0
	}
	return d
}

func luma(r, g, b uint8) uint8 {
	return uint8((299*int(r) + 587*int(g) + 114*int(b)) / 1000)
}

func chroma(r, g, b uint8) uint8 {
	return max(r, g, b) - min(r, g, b)
}

func vertical(edge int) bool { return edge == edgeLeft || edge == edgeRight }

func (d *detection) found(edge int) bool { return d.edges[edge].ok }

// beside is d's paper, filled in from o, the other side of the same sheet,
// as DetectSheet describes.
func (d *detection) beside(o *detection) *Paper {
	if d == nil {
		return nil
	}
	tilt, size := d.tilt, image.Point{}
	if op, ok := o.ownPaper(); ok {
		if d.foundOneOf(edgeLeft, edgeRight) && o.found(edgeLeft) && o.found(edgeRight) {
			size.X = op.Crop.W
		}
		if d.foundOneOf(edgeTop, edgeBottom) && o.found(edgeTop) && o.found(edgeBottom) {
			size.Y = op.Crop.H
		}
		if tilt == 0 && size != (image.Point{}) {
			tilt = -op.Straighten
		}
	}
	p, ok := d.paper(tilt, size)
	if !ok {
		return nil
	}
	return &p
}

func (d *detection) ownPaper() (Paper, bool) {
	if d == nil {
		return Paper{}, false
	}
	return d.paper(d.tilt, image.Point{})
}

func (d *detection) foundOneOf(a, b int) bool { return d.found(a) != d.found(b) }

// paper crops to the edges found, with the scan straightened by tilt, and
// keeps the scan's side where an edge wasn't found. Where size is set, the
// side found only one of the two edges across it, and the other is placed
// size away.
func (d *detection) paper(tilt float64, size image.Point) (Paper, bool) {
	g := d.g
	straightened := tilt != 0
	turn := newTurn(tilt, float64(g.w)/2, float64(g.h)/2)
	crop := rect{0, float64(d.top), float64(g.w), float64(d.bottom)}
	bounds := [4]*float64{&crop.x0, &crop.x1, &crop.y0, &crop.y1}
	for i, e := range d.edges {
		if e.ok {
			*bounds[i] = e.boundary(turn, vertical(i), i == edgeRight || i == edgeBottom, straightened)
		}
	}
	if straightened {
		crop = turn.within(crop, rect{0, float64(d.top), float64(g.w), float64(g.h)})
	}

	sx, sy := float64(d.width)/float64(g.w), float64(d.height)/float64(g.h)
	inset := [4]float64{}
	for i := range d.edges {
		if d.found(i) {
			inset[i] = math.Ceil(max(sx, sy))
		}
	}
	x0 := int(math.Ceil(crop.x0*sx + inset[edgeLeft]))
	x1 := int(math.Floor(crop.x1*sx - inset[edgeRight]))
	y0 := int(math.Ceil(crop.y0*sy + inset[edgeTop]))
	y1 := int(math.Floor(crop.y1*sy - inset[edgeBottom]))
	x0, x1 = fillIn(x0, x1, size.X, d.found(edgeLeft))
	y0, y1 = fillIn(y0, y1, size.Y, d.found(edgeTop))
	x0, y0 = max(x0, 0), max(y0, 0)
	x1, y1 = min(x1, d.width), min(y1, d.height)

	if float64(x1-x0) < minPaperShare*float64(d.width) || float64(y1-y0) < minPaperShare*float64(d.height) {
		return Paper{}, false
	}
	if !straightened && x0 == 0 && y0 == 0 && x1 == d.width && y1 == d.height {
		return Paper{}, false
	}
	return Paper{Straighten: math.Round(tilt*100) / 100, Crop: Crop{X: x0, Y: y0, W: x1 - x0, H: y1 - y0}}, true
}

// fillIn places the one of two opposite crop edges that wasn't found size
// away from the one that was, when size is set.
func fillIn(lo, hi, size int, foundLo bool) (int, int) {
	switch {
	case size == 0:
	case foundLo:
		hi = lo + size
	default:
		lo = hi - size
	}
	return lo, hi
}

// grid holds a downscaled scan's brightness and chroma.
type grid struct {
	w, h     int
	lum, chr []uint8
	back     int
}

func newGrid(img image.Image) *grid {
	var small *image.NRGBA
	if img.Bounds().Dx() > paperScanWidth {
		small = imaging.Resize(img, paperScanWidth, 0, imaging.Box)
	} else {
		small = imaging.Clone(img)
	}
	w, h := small.Rect.Dx(), small.Rect.Dy()
	g := &grid{w: w, h: h, lum: make([]uint8, w*h), chr: make([]uint8, w*h)}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			o := y*small.Stride + x*4
			r, gr, bl := small.Pix[o], small.Pix[o+1], small.Pix[o+2]
			g.lum[y*w+x] = luma(r, gr, bl)
			g.chr[y*w+x] = chroma(r, gr, bl)
		}
	}
	g.back = g.measureBacking()
	return g
}

// measureBacking takes the backing's brightness from the columns near
// each side, where it shows beside any sheet narrower than the scanner.
// They lie just inside the scan's outermost columns, which may show a
// strip unlike the backing.
func (g *grid) measureBacking() int {
	var samples []int
	columns := []int{edgeStrip, edgeStrip + 1, g.w - 2 - edgeStrip, g.w - 1 - edgeStrip}
	for y := 0; y < g.h; y++ {
		for _, x := range columns {
			if x < 0 || x >= g.w {
				continue
			}
			i := y*g.w + x
			if l := int(g.lum[i]); g.chr[i] <= backingChroma && l >= 120 && l <= 215 {
				samples = append(samples, l)
			}
		}
	}
	if len(samples) < g.h/4 {
		return defaultBacking
	}
	slices.Sort(samples)
	return samples[len(samples)/2]
}

func (g *grid) backingAt(x, y int) bool {
	i := y*g.w + x
	l := int(g.lum[i])
	return g.chr[i] <= backingChroma && l >= g.back-backingTolerance && l <= g.back+backingTolerance
}

// startLine is the first row below the dark line the scanner draws
// across the start of every scan, including the row it blurs into.
func (g *grid) startLine() int {
	row := make([]uint8, g.w)
	limit := max(1, g.h/50)
	for y := 0; y < limit; y++ {
		copy(row, g.lum[y*g.w:(y+1)*g.w])
		slices.Sort(row)
		if int(row[g.w/2]) >= g.back-backingTolerance {
			return y
		}
	}
	return limit
}

// edges walks in from each side of every row (and column) that starts on
// the backing, to where the backing ends.
func (g *grid) edges(top, bottom int) [4]*edgeLine {
	left, right, upper, lower := &edgeLine{}, &edgeLine{}, &edgeLine{}, &edgeLine{}
	reachX, reachY := int(edgeSearch*float64(g.w)), int(edgeSearch*float64(bottom-top))
	margin := (bottom - top) / 20
	for y := top + margin; y < bottom-margin; y++ {
		at := float64(y) + 0.5
		if pos, ok := g.walk(0, y, 1, 0, reachX); ok {
			left.add(at, float64(pos))
		}
		if pos, ok := g.walk(g.w-1, y, -1, 0, reachX); ok {
			right.add(at, float64(pos+1))
		}
	}
	for x := g.w / 20; x < g.w-g.w/20; x++ {
		at := float64(x) + 0.5
		if pos, ok := g.walk(x, top, 0, 1, reachY); ok {
			upper.add(at, float64(pos))
		}
		if pos, ok := g.walk(x, bottom-1, 0, -1, reachY); ok {
			lower.add(at, float64(pos+1))
		}
	}
	return [4]*edgeLine{left, right, upper, lower}
}

// walk steps from (x, y) by (dx, dy) and returns the position of the first
// pixel of the sheet, or false if the walk doesn't start on the backing or
// never leaves it. The backing may start up to edgeStrip pixels in, behind
// a strip at the scan's edge, and must show two pixels in a row there, so
// the blurred inner edge of a strip isn't taken for it.
func (g *grid) walk(x, y, dx, dy, reach int) (int, bool) {
	start := 0
	for !g.backingAt(x+dx*start, y+dy*start) || !g.backingAt(x+dx*(start+1), y+dy*(start+1)) {
		if start++; start > edgeStrip {
			return 0, false
		}
	}
	run := 0
	for step := start; step < reach; step++ {
		px, py := x+dx*step, y+dy*step
		if g.backingAt(px, py) {
			run = 0
			continue
		}
		if run++; run == edgeRun {
			if dx != 0 {
				return px - dx*(edgeRun-1), true
			}
			return py - dy*(edgeRun-1), true
		}
	}
	return 0, false
}

// edgeLine is a sheet edge: positions across it, by position along it.
type edgeLine struct {
	at, pos       []float64
	slope, offset float64
	inliers       []int
	ok            bool
}

func (e *edgeLine) add(at, pos float64) {
	e.at = append(e.at, at)
	e.pos = append(e.pos, pos)
}

// fit finds the line through most of the points, ignoring those that
// belong to something else, such as ink or a corner.
func (e *edgeLine) fit(enough float64) bool {
	if float64(len(e.at)) < enough || len(e.at) < 10 {
		return false
	}
	step := max(1, len(e.at)/200)
	var slopes []float64
	for i := 0; i < len(e.at); i += step {
		for j := i + step; j < len(e.at); j += step {
			if d := e.at[j] - e.at[i]; d > 5 {
				slopes = append(slopes, (e.pos[j]-e.pos[i])/d)
			}
		}
	}
	if len(slopes) == 0 {
		return false
	}
	e.slope = median(slopes)
	offsets := make([]float64, len(e.at))
	for i := range e.at {
		offsets[i] = e.pos[i] - e.slope*e.at[i]
	}
	e.offset = median(offsets)

	for range 2 {
		e.inliers = e.inliers[:0]
		for i := range e.at {
			if math.Abs(e.pos[i]-e.slope*e.at[i]-e.offset) <= edgeInlier {
				e.inliers = append(e.inliers, i)
			}
		}
		if float64(len(e.inliers)) < enough || len(e.inliers) < 10 {
			return false
		}
		e.slope, e.offset = leastSquares(e.at, e.pos, e.inliers)
	}
	e.ok = true
	return true
}

func leastSquares(at, pos []float64, use []int) (slope, offset float64) {
	var sa, sp, saa, sap float64
	for _, i := range use {
		sa += at[i]
		sp += pos[i]
		saa += at[i] * at[i]
		sap += at[i] * pos[i]
	}
	n := float64(len(use))
	if d := n*saa - sa*sa; d != 0 {
		slope = (n*sap - sa*sp) / d
	}
	return slope, (sp - slope*sa) / n
}

// boundary is where the edge lies once the scan is straightened, a little
// toward the inside so no sliver of backing is left. An edge too crooked to
// straighten is bounded on its outside instead, so none of the sheet is
// cropped away.
func (e *edgeLine) boundary(t turn, vertical, far, straightened bool) float64 {
	var across []float64
	for _, i := range e.inliers {
		x, y := e.pos[i], e.at[i]
		if !vertical {
			x, y = e.at[i], e.pos[i]
		}
		sx, sy := t.straighten(x, y)
		if vertical {
			across = append(across, sx)
		} else {
			across = append(across, sy)
		}
	}
	slices.Sort(across)
	n := len(across)
	crooked := across[n*9/10]-across[n/10] > 3
	switch {
	case crooked && !straightened && far:
		return across[n-1]
	case crooked && !straightened:
		return across[0]
	case far:
		return across[n/4]
	default:
		return across[n*3/4]
	}
}

type weighted struct{ value, weight float64 }

// consensus is the weighted mean of the tilts that agree with the best
// supported one.
func consensus(tilts []weighted) float64 {
	if len(tilts) == 0 {
		return 0
	}
	best := slices.MaxFunc(tilts, func(a, b weighted) int { return int(a.weight - b.weight) })
	var sum, weight float64
	for _, t := range tilts {
		if math.Abs(t.value-best.value) <= 0.5 {
			sum += t.value * t.weight
			weight += t.weight
		}
	}
	return sum / weight
}

type rect struct{ x0, y0, x1, y1 float64 }

// turn maps between the scan and the scan straightened counter-clockwise
// by deg about (cx, cy), as Render does.
type turn struct{ sin, cos, cx, cy float64 }

func newTurn(deg, cx, cy float64) turn {
	sin, cos := math.Sincos(deg * math.Pi / 180)
	return turn{sin, cos, cx, cy}
}

func (t turn) straighten(x, y float64) (float64, float64) {
	dx, dy := x-t.cx, y-t.cy
	return t.cx + dx*t.cos + dy*t.sin, t.cy - dx*t.sin + dy*t.cos
}

func (t turn) unstraighten(x, y float64) (float64, float64) {
	dx, dy := x-t.cx, y-t.cy
	return t.cx + dx*t.cos - dy*t.sin, t.cy + dx*t.sin + dy*t.cos
}

// within shrinks r, a rectangle in the straightened frame, until every
// corner falls inside the scanned area, so the crop shows no filler.
func (t turn) within(r, scanned rect) rect {
	const step = 0.5
	for range 4000 {
		moved := false
		for _, c := range [4][2]bool{{false, false}, {true, false}, {false, true}, {true, true}} {
			cx, cy := r.x0, r.y0
			if c[0] {
				cx = r.x1
			}
			if c[1] {
				cy = r.y1
			}
			x, y := t.unstraighten(cx, cy)
			if x < scanned.x0-0.5 || x > scanned.x1+0.5 {
				moved = true
				if c[0] {
					r.x1 -= step
				} else {
					r.x0 += step
				}
			}
			if y < scanned.y0-0.5 || y > scanned.y1+0.5 {
				moved = true
				if c[1] {
					r.y1 -= step
				} else {
					r.y0 += step
				}
			}
		}
		if !moved || r.x1-r.x0 < 1 || r.y1-r.y0 < 1 {
			break
		}
	}
	return r
}

func median(v []float64) float64 {
	s := slices.Clone(v)
	slices.Sort(s)
	return s[len(s)/2]
}
