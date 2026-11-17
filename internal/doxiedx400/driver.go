package doxiedx400

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/jpeg"
	"time"

	"github.com/andiapps-dev/doxie-scanner/internal/driver"
	"github.com/andiapps-dev/doxie-scanner/internal/scsiusb"
	"github.com/disintegration/imaging"
)

const defaultResolution = 300

func init() {
	driver.Register("doxie-dx400", New)
}

// New constructs a Doxie Pro DX400 driver.Driver.
func New() driver.Driver { return &dxDriver{} }

type dxDriver struct{}

func (d *dxDriver) Info() driver.Info {
	return driver.Info{
		Name:   "doxie-dx400",
		Vendor: "Avision (Doxie)",
		Model:  "Doxie Pro DX400",
		VID:    VID,
		PID:    PID,
	}
}

// openDeviceFunc is swapped out by tests for a fake device.
var openDeviceFunc = func() (scsiusb.BulkDevice, error) {
	return scsiusb.Open(VID, PID, usbInterfaceNum, epBulkIn&0x0f, epBulkOut&0x0f, epInterrupt&0x0f)
}

// Detect reports whether the scanner can be opened right now. There is no
// cheaper probe than opening it.
func (d *dxDriver) Detect(ctx context.Context) error {
	dev, err := openDeviceFunc()
	if err != nil {
		return err
	}
	return dev.Close()
}

func (d *dxDriver) Open(ctx context.Context) (driver.Session, error) {
	dev, err := openDeviceFunc()
	if err != nil {
		return nil, err
	}
	return &session{dev: dev, sleep: time.Sleep}, nil
}

// Within a stack, the next sheet can take a moment to reach the paper
// sensor after the last one leaves.
const (
	nextSheetWait = time.Second
	nextSheetPoll = 100 * time.Millisecond
)

// session follows Doxie's driver, which keeps the scan job reserved for a
// whole stack and only releases the scan unit between sheets. Releasing
// the job after every sheet left the next sheet half fed.
type session struct {
	dev   scsiusb.BulkDevice
	batch *window // the open stack's settings; nil between stacks
	sleep func(time.Duration)
}

// HasNextPage reports whether paper is waiting. Inside a stack, "no
// paper" only counts once it has lasted nextSheetWait.
func (s *session) HasNextPage(ctx context.Context) (bool, error) {
	paper, err := hasPaper(s.dev)
	for waited := time.Duration(0); err == nil && !paper && s.batch != nil && waited < nextSheetWait; waited += nextSheetPoll {
		s.sleep(nextSheetPoll)
		paper, err = hasPaper(s.dev)
	}
	return paper, err
}

// side is one side of a sheet exactly as the scanner delivered it.
type side struct {
	JPEG []byte // a complete JPEG file
	// XDPI and YDPI are the sampling resolution. They differ when the
	// scanner reads fewer lines per inch than columns (200 dpi is read at
	// 300 across), so the pixels must be squared up before use.
	XDPI, YDPI int
}

// sheet is what one trip of a sheet through the scanner yields.
type sheet struct {
	Front side
	Back  *side // nil only for a simplex scan
}

// scanSides scans the next sheet and returns each side as the scanner's
// own JPEG.
func (s *session) scanSides(ctx context.Context, opts driver.ScanOptions) (sheet, error) {
	res := opts.Resolution
	if res == 0 {
		res = defaultResolution
	}
	w, err := newWindow(res, opts.Duplex)
	if err != nil {
		return sheet{}, err
	}
	sides, err := s.scanSheet(w, quantTables[res])
	if err != nil {
		_ = s.EndBatch(ctx) // the scan error is what the person needs to see
		return sheet{}, err
	}

	mk := func(jpg []byte) side { return side{JPEG: jpg, XDPI: w.xRes, YDPI: w.yRes} }
	out := sheet{Front: mk(sides[0])}
	if opts.Duplex {
		back := mk(sides[1])
		out.Back = &back
	}
	return out, nil
}

// ScanPage scans the next sheet and decodes each side. A duplex back with
// nothing printed on it is reported as BackBlank instead of being returned.
func (s *session) ScanPage(ctx context.Context, opts driver.ScanOptions) (driver.Page, error) {
	sh, err := s.scanSides(ctx, opts)
	if err != nil {
		return driver.Page{}, err
	}
	front, err := decode(sh.Front)
	if err != nil {
		return driver.Page{}, fmt.Errorf("doxiedx400: front side: %w", err)
	}
	page := driver.Page{Front: front}
	if sh.Back == nil {
		return page, nil
	}
	back, err := decode(*sh.Back)
	if err != nil {
		return driver.Page{}, fmt.Errorf("doxiedx400: back side: %w", err)
	}
	if isBlank(back) {
		page.BackBlank = true
	} else {
		page.Back = back
	}
	return page, nil
}

// decode turns a side's JPEG into square pixels.
func decode(sd side) (*image.NRGBA, error) {
	img, err := jpeg.Decode(bytes.NewReader(sd.JPEG))
	if err != nil {
		return nil, err
	}
	out := image.NewNRGBA(img.Bounds())
	draw.Draw(out, out.Bounds(), img, img.Bounds().Min, draw.Src)
	if sd.XDPI != sd.YDPI && sd.XDPI > 0 && sd.YDPI > 0 {
		// 200 dpi is read at 300 across: shrink the width to match the
		// lines, so a pixel is as wide as it is tall.
		b := out.Bounds()
		return imaging.Resize(out, b.Dx()*sd.YDPI/sd.XDPI, b.Dy(), imaging.Lanczos), nil
	}
	return out, nil
}

// A back is blank when under 2% of its pixels are darker than a mean gray
// of 180. Measured on real scans: genuine blank backs (show-through and
// shading only) had 0.3% to 1.2% dark, printed pages 5% or more. A faint
// printed back could land in between and be dropped; that is the accepted
// trade-off.
const (
	blankDarkThreshold        = 180
	blankInkFractionThreshold = 0.02
)

func isBlank(img *image.NRGBA) bool {
	total := img.Bounds().Dx() * img.Bounds().Dy()
	if total == 0 {
		return true
	}
	dark := 0
	for y := 0; y < img.Bounds().Dy(); y++ {
		row := img.Pix[y*img.Stride:]
		for x := 0; x < img.Bounds().Dx(); x++ {
			o := x * 4
			if (float64(row[o])+float64(row[o+1])+float64(row[o+2]))/3.0 < blankDarkThreshold {
				dark++
			}
		}
	}
	return float64(dark)/float64(total) < blankInkFractionThreshold
}

// WaitForButton listens on the interrupt pipe, where a press arrives as
// an 8-byte packet whose first byte has the 0x80 "pressed" flag set. The
// app doesn't use it yet; it is here for starting a scan from the
// scanner's own button.
func (s *session) WaitForButton(ctx context.Context, timeout time.Duration) (bool, error) {
	ir, ok := s.dev.(scsiusb.InterruptReader)
	if !ok {
		return false, nil
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	buf := make([]byte, buttonPacketLen)
	n, err := ir.ReadInterrupt(ctx, buf)
	if err != nil {
		if ctx.Err() != nil {
			return false, nil
		}
		return false, err
	}
	return n > 0 && buf[0]&buttonPressed != 0, nil
}

// scanSheet starts the batch with the full setup on its first sheet and
// runs the shorter one before every later sheet.
func (s *session) scanSheet(w window, quant []byte) ([][]byte, error) {
	if s.batch != nil && *s.batch != w {
		if err := s.EndBatch(context.Background()); err != nil {
			return nil, err
		}
	}
	if s.batch == nil {
		if err := control(s.dev, opReserve, flagJob); err != nil {
			return nil, err
		}
		s.batch = &w
		if err := prepare(s.dev, w, quant); err != nil {
			return nil, err
		}
	} else if err := prepareNext(s.dev, w); err != nil {
		return nil, err
	}
	return scanSheet(s.dev, w, quant)
}

// EndBatch releases the scan job held for the stack.
func (s *session) EndBatch(ctx context.Context) error {
	if s.batch == nil {
		return nil
	}
	s.batch = nil
	return control(s.dev, opRelease, flagJob)
}

// Close ends any open stack, then releases the device.
func (s *session) Close() error {
	return errors.Join(s.EndBatch(context.Background()), s.dev.Close())
}
