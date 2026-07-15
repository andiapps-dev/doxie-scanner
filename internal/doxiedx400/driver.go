package doxiedx400

import (
	"context"

	"github.com/andiapps-dev/doxie-scanner/internal/driver"
	"github.com/andiapps-dev/doxie-scanner/internal/scsiusb"
)

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

// openDeviceFunc is a package-level indirection so tests can substitute a
// fake scsiusb.BulkDevice without touching real hardware; production
// code never reassigns it. This keeps the *only* line that actually
// calls scsiusb.Open (and therefore real gousb/libusb) tiny and
// isolated, while everything that builds on top of a BulkDevice —
// which is effectively all of this driver's real logic — stays fully
// testable.
var openDeviceFunc = func() (scsiusb.BulkDevice, error) {
	return scsiusb.Open(VID, PID, usbInterfaceNum, epBulkIn&0x0f, epBulkOut&0x0f)
}

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
	return &session{dev: dev}, nil
}

// session implements driver.Session against any scsiusb.BulkDevice —
// real hardware in production, a fake in tests.
type session struct {
	dev scsiusb.BulkDevice
}

func (s *session) HasNextPage(ctx context.Context) (bool, error) {
	return hasPaper(s.dev)
}

func (s *session) ScanPage(ctx context.Context, opts driver.ScanOptions) (driver.Page, error) {
	chunks, err := scanOnePage(s.dev, opts.Duplex)
	if err != nil {
		return driver.Page{}, err
	}

	if !opts.Duplex {
		return driver.Page{Front: rawToImage(joinChunks(chunks))}, nil
	}

	front, rear := splitDuplexChunks(chunks)
	page := driver.Page{Front: rawToImage(front)}
	if isBlank(rear) {
		page.BackBlank = true
	} else {
		page.Back = rawToImage(rear)
	}
	return page, nil
}

func (s *session) Close() error {
	return s.dev.Close()
}

func joinChunks(chunks [][]byte) []byte {
	total := 0
	for _, c := range chunks {
		total += len(c)
	}
	out := make([]byte, 0, total)
	for _, c := range chunks {
		out = append(out, c...)
	}
	return out
}
