package doxiedx400

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/jpeg"
	"testing"

	"github.com/andiapps-dev/doxie-scanner/internal/driver"
	"github.com/andiapps-dev/doxie-scanner/internal/scsiusb"
)

func withFakeOpenDevice(t *testing.T, dev scsiusb.BulkDevice, err error) {
	t.Helper()
	original := openDeviceFunc
	openDeviceFunc = func() (scsiusb.BulkDevice, error) { return dev, err }
	t.Cleanup(func() { openDeviceFunc = original })
}

func openFake(t *testing.T, dev *fakeDevice) *session {
	t.Helper()
	withFakeOpenDevice(t, dev, nil)
	sess, err := New().Open(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return sess.(*session)
}

func TestDriverInfo(t *testing.T) {
	info := New().Info()
	if info.Name != "doxie-dx400" || info.VID != VID || info.PID != PID {
		t.Errorf("got %+v", info)
	}
}

func TestOpenError(t *testing.T) {
	withFakeOpenDevice(t, nil, errors.New("no device"))
	if _, err := New().Open(context.Background()); err == nil {
		t.Error("expected the open error")
	}
}

func TestHasNextPage(t *testing.T) {
	dev := &fakeDevice{t: t}
	dev.ok([]byte{0x00, 0x00, 0x02, 0x05, 0x01})
	ok, err := openFake(t, dev).HasNextPage(context.Background())
	if err != nil || !ok {
		t.Fatalf("got %v, %v", ok, err)
	}
}

func TestHasNextPage_WaitsForTheNextSheetInsideAStack(t *testing.T) {
	dev := &fakeDevice{t: t}
	dev.scriptFirstSheet(false, scriptedSide{filled(0x11, 10), 1})
	dev.paper(false)
	dev.paper(false)
	dev.paper(true)
	sess, sleeps := testSession(t, dev)
	ctx := context.Background()
	if _, err := sess.scanSides(ctx, driver.ScanOptions{}); err != nil {
		t.Fatal(err)
	}

	if more, err := sess.HasNextPage(ctx); err != nil || !more || *sleeps != 2 {
		t.Fatalf("got %v, %v after %d waits; want the sheet found after 2", more, err, *sleeps)
	}
}

func TestHasNextPage_EmptyFeederEndsTheWait(t *testing.T) {
	dev := &fakeDevice{t: t}
	dev.scriptFirstSheet(false, scriptedSide{filled(0x11, 10), 1})
	polls := int(nextSheetWait / nextSheetPoll)
	for i := 0; i <= polls; i++ {
		dev.paper(false)
	}
	sess, sleeps := testSession(t, dev)
	ctx := context.Background()
	if _, err := sess.scanSides(ctx, driver.ScanOptions{}); err != nil {
		t.Fatal(err)
	}

	if more, err := sess.HasNextPage(ctx); err != nil || more || *sleeps != polls || len(dev.reads) != 0 {
		t.Fatalf("got %v, %v after %d waits; want no paper after %d", more, err, *sleeps, polls)
	}
}

func TestHasNextPage_OutsideAStackChecksOnce(t *testing.T) {
	dev := &fakeDevice{t: t}
	dev.paper(false)
	sess, sleeps := testSession(t, dev)
	if more, err := sess.HasNextPage(context.Background()); err != nil || more || *sleeps != 0 {
		t.Fatalf("got %v, %v after %d waits", more, err, *sleeps)
	}
}

func TestScanPage_NewSettingsStartANewStack(t *testing.T) {
	side := scriptedSide{filled(0x11, 10), 1}
	dev := &fakeDevice{t: t}
	dev.scriptFirstSheet(false, side)
	dev.ok(nil) // E7 job
	dev.scriptFirstSheet(false, side)
	sess, _ := testSession(t, dev)
	ctx := context.Background()

	for _, res := range []int{300, 600} {
		if _, err := sess.scanSides(ctx, driver.ScanOptions{Resolution: res}); err != nil {
			t.Fatalf("%d dpi: %v", res, err)
		}
	}
	reserves := 0
	for _, op := range dev.opcodes() {
		if op == [3]byte{opReserve, 0, flagJob} {
			reserves++
		}
	}
	if reserves != 2 || len(dev.reads) != 0 {
		t.Errorf("reserved the job %d times with %d replies unused; want a fresh job for new settings", reserves, len(dev.reads))
	}
}

func TestEndBatch_WithoutAStackSendsNothing(t *testing.T) {
	dev := &fakeDevice{t: t}
	sess, _ := testSession(t, dev)
	if err := sess.EndBatch(context.Background()); err != nil || len(dev.commands) != 0 {
		t.Fatalf("got %v with %d commands", err, len(dev.commands))
	}
}

func TestSessionClose_EndsAnOpenStack(t *testing.T) {
	dev := &fakeDevice{t: t}
	dev.scriptFirstSheet(false, scriptedSide{filled(0x11, 10), 1})
	dev.ok(nil) // E7 job
	sess, _ := testSession(t, dev)
	if _, err := sess.scanSides(context.Background(), driver.ScanOptions{}); err != nil {
		t.Fatal(err)
	}

	if err := sess.Close(); err != nil || !dev.closed {
		t.Fatalf("close: %v, closed=%v", err, dev.closed)
	}
	ops := dev.opcodes()
	if last := ops[len(ops)-1]; last != [3]byte{opRelease, 0, flagJob} {
		t.Errorf("last command %x, want the job released before closing", last)
	}
}

func decodes(t *testing.T, s side) image.Point {
	t.Helper()
	img, err := jpeg.Decode(bytes.NewReader(s.JPEG))
	if err != nil {
		t.Fatalf("side does not decode: %v", err)
	}
	return img.Bounds().Size()
}

func TestScanPage_DuplexReturnsBothSidesEvenWhenBlank(t *testing.T) {
	dev := &fakeDevice{t: t}
	dev.scriptFirstSheet(true,
		scriptedSide{fixture(t, "ink-300.scan"), 32},
		scriptedSide{fixture(t, "blank-300.scan"), 32})

	page, err := openFake(t, dev).scanSides(context.Background(), driver.ScanOptions{Duplex: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := decodes(t, page.Front); got != (image.Point{2550, 32}) {
		t.Errorf("front: %v", got)
	}
	if page.Back == nil {
		t.Fatal("a blank back must still be returned; dropping it is the caller's decision")
	}
	decodes(t, *page.Back)
	if page.Front.XDPI != 300 || page.Front.YDPI != 300 {
		t.Errorf("dpi: %dx%d", page.Front.XDPI, page.Front.YDPI)
	}
}

func TestScanPage_200dpiReportsNonSquareSampling(t *testing.T) {
	dev := &fakeDevice{t: t}
	dev.scriptFirstSheet(false, scriptedSide{fixture(t, "ink-300.scan"), 32})

	page, err := openFake(t, dev).scanSides(context.Background(), driver.ScanOptions{Resolution: 200})
	if err != nil {
		t.Fatal(err)
	}
	if page.Front.XDPI != 300 || page.Front.YDPI != 200 {
		t.Errorf("200 dpi is read at 300 across: got %dx%d", page.Front.XDPI, page.Front.YDPI)
	}
}

func TestScanPage_SimplexDefaultsTo300(t *testing.T) {
	dev := &fakeDevice{t: t}
	dev.scriptFirstSheet(false, scriptedSide{fixture(t, "ink-300.scan"), 32})

	page, err := openFake(t, dev).scanSides(context.Background(), driver.ScanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if page.Back != nil {
		t.Error("simplex scan should have no back")
	}
	window := dev.payloads[0]
	if window[offYRes] != 0x01 || window[offYRes+1] != 0x2c {
		t.Errorf("default resolution should be 300 dpi, window yres % x", window[offYRes:offYRes+2])
	}
}

func TestScanPage_RejectsUnsupportedResolution(t *testing.T) {
	dev := &fakeDevice{t: t}
	if _, err := openFake(t, dev).scanSides(context.Background(), driver.ScanOptions{Resolution: 150}); err == nil {
		t.Fatal("expected 150 dpi to be rejected")
	}
	if len(dev.commands) != 0 {
		t.Errorf("nothing should be sent for an invalid resolution, sent %d commands", len(dev.commands))
	}
}

func TestScanPage_SurfacesScanErrors(t *testing.T) {
	dev := &fakeDevice{t: t}
	dev.checkCondition(nil, senseBlock(ascADF, ascqChuteEmpty, 0, 0, 0))
	if _, err := openFake(t, dev).scanSides(context.Background(), driver.ScanOptions{}); !errors.Is(err, ErrNoPaper) {
		t.Fatalf("got %v, want ErrNoPaper", err)
	}
}

func TestSessionClose(t *testing.T) {
	dev := &fakeDevice{t: t}
	if err := openFake(t, dev).Close(); err != nil || !dev.closed {
		t.Fatalf("close: %v, closed=%v", err, dev.closed)
	}
}
