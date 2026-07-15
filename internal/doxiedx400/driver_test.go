package doxiedx400

import (
	"context"
	"errors"
	"image"
	"testing"

	"github.com/andiapps-dev/doxie-scanner/internal/driver"
	"github.com/andiapps-dev/doxie-scanner/internal/scsiusb"
)

// withFakeOpenDevice temporarily swaps openDeviceFunc to return the given
// device/error, restoring the original (real, hardware-backed) function
// when the test finishes.
func withFakeOpenDevice(t *testing.T, dev scsiusb.BulkDevice, err error) {
	t.Helper()
	original := openDeviceFunc
	openDeviceFunc = func() (scsiusb.BulkDevice, error) {
		return dev, err
	}
	t.Cleanup(func() { openDeviceFunc = original })
}

func TestDriverInfo(t *testing.T) {
	info := New().Info()
	if info.Name != "doxie-dx400" {
		t.Errorf("Name: got %q", info.Name)
	}
	if info.VID != VID || info.PID != PID {
		t.Errorf("VID/PID: got %#04x/%#04x", info.VID, info.PID)
	}
}

func TestDxDriver_Detect_Success(t *testing.T) {
	dev := &fakeDevice{t: t}
	withFakeOpenDevice(t, dev, nil)

	d := New()
	if err := d.Detect(context.Background()); err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if !dev.closed {
		t.Error("expected Detect to close the device after probing it")
	}
}

func TestDxDriver_Detect_OpenError(t *testing.T) {
	withFakeOpenDevice(t, nil, errors.New("no device"))

	d := New()
	if err := d.Detect(context.Background()); err == nil {
		t.Fatal("expected an error")
	}
}

func TestDxDriver_Detect_CloseErrorPropagates(t *testing.T) {
	dev := &fakeDevice{t: t, closeErr: errors.New("close failed")}
	withFakeOpenDevice(t, dev, nil)

	d := New()
	if err := d.Detect(context.Background()); err == nil {
		t.Fatal("expected the close error to propagate")
	}
}

func TestDxDriver_Open_Success(t *testing.T) {
	dev := &fakeDevice{t: t}
	withFakeOpenDevice(t, dev, nil)

	d := New()
	sess, err := d.Open(context.Background())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if sess == nil {
		t.Fatal("expected a non-nil session")
	}
}

func TestDxDriver_Open_Error(t *testing.T) {
	withFakeOpenDevice(t, nil, errors.New("no device"))

	d := New()
	if _, err := d.Open(context.Background()); err == nil {
		t.Fatal("expected an error")
	}
}

func TestSession_HasNextPage(t *testing.T) {
	dev := &fakeDevice{t: t}
	dev.queueOKCommand([]byte{0x01})
	sess := &session{dev: dev}

	ok, err := sess.HasNextPage(context.Background())
	if err != nil {
		t.Fatalf("HasNextPage: %v", err)
	}
	if !ok {
		t.Error("expected true")
	}
}

func TestSession_HasNextPage_Error(t *testing.T) {
	sess := &session{dev: &alwaysFailDevice{}}
	if _, err := sess.HasNextPage(context.Background()); err == nil {
		t.Fatal("expected an error")
	}
}

func TestSession_ScanPage_Simplex(t *testing.T) {
	dev := &fakeDevice{t: t}
	scriptFullPageSetup(dev)
	line := make([]byte, lineWidthBytes)
	line[0], line[1], line[2] = 0x10, 0x20, 0x30
	dev.queueOKCommand(line)
	dev.queueEndOfDocumentImageRead()
	dev.queueOKCommand(make([]byte, senseBufferLen))
	dev.queueGoodStatus()
	dev.queueGoodStatus()

	sess := &session{dev: dev}
	page, err := sess.ScanPage(context.Background(), driver.ScanOptions{Duplex: false})
	if err != nil {
		t.Fatalf("ScanPage: %v", err)
	}
	if page.Front == nil {
		t.Fatal("expected a non-nil Front image")
	}
	if page.Back != nil {
		t.Error("expected a nil Back image for a simplex scan")
	}
	if page.Front.Bounds().Dy() != 1 {
		t.Errorf("expected 1 scanline, got %d", page.Front.Bounds().Dy())
	}
}

func TestSession_ScanPage_DuplexNonBlankBack(t *testing.T) {
	dev := &fakeDevice{t: t}
	scriptFullPageSetup(dev)
	// Two stripes: front (even index) then rear (odd index), each one
	// full scanline. Rear has plenty of dark pixels so it isn't blank.
	frontLine := make([]byte, lineWidthBytes)
	for i := range frontLine {
		frontLine[i] = 250
	}
	rearLine := make([]byte, lineWidthBytes)
	// leave rearLine all zero (black): clearly not blank
	dev.queueOKCommand(frontLine)
	dev.queueOKCommand(rearLine)
	dev.queueEndOfDocumentImageRead()
	dev.queueOKCommand(make([]byte, senseBufferLen))
	dev.queueGoodStatus()
	dev.queueGoodStatus()

	sess := &session{dev: dev}
	page, err := sess.ScanPage(context.Background(), driver.ScanOptions{Duplex: true})
	if err != nil {
		t.Fatalf("ScanPage: %v", err)
	}
	if page.Front == nil || page.Back == nil {
		t.Fatal("expected both Front and Back images")
	}
	if page.BackBlank {
		t.Error("expected BackBlank to be false")
	}
}

func TestSession_ScanPage_DuplexBlankBackDropped(t *testing.T) {
	dev := &fakeDevice{t: t}
	scriptFullPageSetup(dev)
	frontLine := make([]byte, lineWidthBytes)
	blankRearLine := make([]byte, lineWidthBytes)
	for i := range blankRearLine {
		blankRearLine[i] = 250 // near-white: blank
	}
	dev.queueOKCommand(frontLine)
	dev.queueOKCommand(blankRearLine)
	dev.queueEndOfDocumentImageRead()
	dev.queueOKCommand(make([]byte, senseBufferLen))
	dev.queueGoodStatus()
	dev.queueGoodStatus()

	sess := &session{dev: dev}
	page, err := sess.ScanPage(context.Background(), driver.ScanOptions{Duplex: true})
	if err != nil {
		t.Fatalf("ScanPage: %v", err)
	}
	if page.Front == nil {
		t.Fatal("expected a non-nil Front image")
	}
	if page.Back != nil {
		t.Error("expected Back to be nil when the rear side is blank")
	}
	if !page.BackBlank {
		t.Error("expected BackBlank to be true")
	}
}

func TestSession_ScanPage_Error(t *testing.T) {
	sess := &session{dev: &alwaysFailDevice{}}
	if _, err := sess.ScanPage(context.Background(), driver.ScanOptions{}); err == nil {
		t.Fatal("expected an error")
	}
}

func TestSession_Close(t *testing.T) {
	dev := &fakeDevice{t: t}
	sess := &session{dev: dev}
	if err := sess.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !dev.closed {
		t.Error("expected the underlying device to be closed")
	}
}

func TestJoinChunks(t *testing.T) {
	if got := joinChunks(nil); len(got) != 0 {
		t.Errorf("empty input: got %v", got)
	}
	got := joinChunks([][]byte{{1, 2}, {3}, {4, 5, 6}})
	want := []byte{1, 2, 3, 4, 5, 6}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("byte %d: got %d, want %d", i, got[i], want[i])
		}
	}
}

var _ image.Image = (*image.NRGBA)(nil) // sanity: rawToImage's return type satisfies driver.Page.Front
