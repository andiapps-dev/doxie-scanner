package doxiedx400

import (
	"context"
	"errors"
	"image"
	"strings"
	"testing"
	"time"

	"github.com/andiapps-dev/doxie-scanner/internal/driver"
)

// The captured fixtures are 32 lines of a 300 dpi scan: ink-300.scan holds
// printing, blank-300.scan only the paper's own shading.
const fixtureLines = 32

func inkSide(t *testing.T) scriptedSide {
	return scriptedSide{fixture(t, "ink-300.scan"), fixtureLines}
}
func blankSide(t *testing.T) scriptedSide {
	return scriptedSide{fixture(t, "blank-300.scan"), fixtureLines}
}

func TestScanPage_DecodesBothSidesOfAPrintedSheet(t *testing.T) {
	dev := &fakeDevice{t: t}
	dev.scriptFirstSheet(true, inkSide(t), inkSide(t))
	sess, _ := testSession(t, dev)

	page, err := sess.ScanPage(context.Background(), driver.ScanOptions{Duplex: true})
	if err != nil {
		t.Fatal(err)
	}
	if page.BackBlank || page.Back == nil {
		t.Fatalf("a printed back was dropped: blank=%v back=%v", page.BackBlank, page.Back)
	}
	for name, img := range map[string]image.Image{"front": page.Front, "back": page.Back} {
		if b := img.Bounds(); b.Dx() != 2550 || b.Dy() != fixtureLines {
			t.Errorf("%s is %v, want 2550x%d", name, b, fixtureLines)
		}
	}
}

func TestScanPage_ReportsABlankBackInsteadOfReturningIt(t *testing.T) {
	dev := &fakeDevice{t: t}
	dev.scriptFirstSheet(true, inkSide(t), blankSide(t))
	sess, _ := testSession(t, dev)

	page, err := sess.ScanPage(context.Background(), driver.ScanOptions{Duplex: true})
	if err != nil {
		t.Fatal(err)
	}
	if !page.BackBlank || page.Back != nil {
		t.Errorf("a blank back should be reported, not returned: blank=%v back=%v", page.BackBlank, page.Back)
	}
	if page.Front == nil {
		t.Error("the front is always returned")
	}
}

func TestScanPage_SimplexHasNoBack(t *testing.T) {
	dev := &fakeDevice{t: t}
	dev.scriptFirstSheet(false, inkSide(t))
	sess, _ := testSession(t, dev)

	page, err := sess.ScanPage(context.Background(), driver.ScanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if page.Back != nil || page.BackBlank {
		t.Errorf("simplex: back=%v blank=%v", page.Back, page.BackBlank)
	}
}

func TestScanPage_SquaresUpThePixelsAt200dpi(t *testing.T) {
	// 200 dpi is read at 300 across, so the 2550 columns are shrunk to
	// 2550*200/300 = 1700 to match the lines: a pixel as wide as it is tall.
	dev := &fakeDevice{t: t}
	dev.scriptFirstSheet(false, inkSide(t))
	sess, _ := testSession(t, dev)

	page, err := sess.ScanPage(context.Background(), driver.ScanOptions{Resolution: 200})
	if err != nil {
		t.Fatal(err)
	}
	if b := page.Front.Bounds(); b.Dx() != 1700 || b.Dy() != fixtureLines {
		t.Errorf("200 dpi page is %v, want 1700x%d", b, fixtureLines)
	}
}

func TestScanPage_Errors(t *testing.T) {
	junk := scriptedSide{filled(0x11, 100), 10} // not scan data: the JPEG won't decode
	cases := map[string]struct {
		sides  []scriptedSide
		duplex bool
		want   string
	}{
		"front won't decode": {[]scriptedSide{junk}, false, "front side"},
		"back won't decode":  {[]scriptedSide{inkSide(t), junk}, true, "back side"},
	}
	for name, c := range cases {
		dev := &fakeDevice{t: t}
		dev.scriptFirstSheet(c.duplex, c.sides...)
		sess, _ := testSession(t, dev)
		if _, err := sess.ScanPage(context.Background(), driver.ScanOptions{Duplex: c.duplex}); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want one mentioning %q", name, err, c.want)
		}
	}

	// A scanner fault comes through as it is, and an unsupported resolution
	// is refused before the scanner is touched.
	dev := &fakeDevice{t: t, readErr: errors.New("usb gone")}
	sess, _ := testSession(t, dev)
	if _, err := sess.ScanPage(context.Background(), driver.ScanOptions{}); err == nil {
		t.Error("a scanner failure should be returned")
	}
	if _, err := sess.ScanPage(context.Background(), driver.ScanOptions{Resolution: 150}); err == nil {
		t.Error("150 dpi is not supported")
	}
}

func TestDetect(t *testing.T) {
	dev := &fakeDevice{t: t}
	withFakeOpenDevice(t, dev, nil)
	if err := New().Detect(context.Background()); err != nil || !dev.closed {
		t.Errorf("Detect err=%v, device closed=%v: it should open and close the scanner", err, dev.closed)
	}
	withFakeOpenDevice(t, nil, errors.New("not plugged in"))
	if err := New().Detect(context.Background()); err == nil {
		t.Error("Detect should report that the scanner can't be opened")
	}
}

func TestIsBlank_AnEmptyImageIsBlank(t *testing.T) {
	if !isBlank(image.NewNRGBA(image.Rect(0, 0, 0, 0))) {
		t.Error("an empty image should count as blank")
	}
}

// buttonDevice is a fake scanner that also has an interrupt pipe, where a
// press of the scan button shows up as an 8-byte packet.
type buttonDevice struct {
	*fakeDevice
	packet []byte
	err    error
}

func (b *buttonDevice) ReadInterrupt(ctx context.Context, p []byte) (int, error) {
	if b.err != nil {
		return 0, b.err
	}
	if b.packet == nil {
		<-ctx.Done() // nothing pressed: wait out the timeout
		return 0, ctx.Err()
	}
	return copy(p, b.packet), nil
}

func TestWaitForButton(t *testing.T) {
	ctx := context.Background()
	press := []byte{buttonPressed, 0, 0, 0, 0, 0, 0, 0}
	idle := []byte{0, 0, 0, 0, 0, 0, 0, 0}

	for name, c := range map[string]struct {
		dev  *buttonDevice
		want bool
		err  bool
	}{
		"pressed":                 {&buttonDevice{fakeDevice: &fakeDevice{t: t}, packet: press}, true, false},
		"a packet, not a press":   {&buttonDevice{fakeDevice: &fakeDevice{t: t}, packet: idle}, false, false},
		"nothing within the wait": {&buttonDevice{fakeDevice: &fakeDevice{t: t}}, false, false},
		"the pipe fails":          {&buttonDevice{fakeDevice: &fakeDevice{t: t}, err: errors.New("usb gone")}, false, true},
	} {
		sess := &session{dev: c.dev, sleep: func(time.Duration) {}}
		got, err := sess.WaitForButton(ctx, 30*time.Millisecond)
		if got != c.want || (err != nil) != c.err {
			t.Errorf("%s: got %v, %v; want %v (error %v)", name, got, err, c.want, c.err)
		}
	}

	// A device with no interrupt pipe simply never reports a press.
	plain := &session{dev: &fakeDevice{t: t}, sleep: func(time.Duration) {}}
	if got, err := plain.WaitForButton(ctx, 10*time.Millisecond); got || err != nil {
		t.Errorf("no interrupt pipe: got %v, %v", got, err)
	}
}
