package doxiedx400

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/andiapps-dev/doxie-scanner/internal/scsiusb"
)

// pressOnce is a scanner whose button is pressed on the first read only.
type pressOnce struct {
	*fakeDevice
	pressed atomic.Bool
}

func (p *pressOnce) ReadInterrupt(ctx context.Context, b []byte) (int, error) {
	if p.pressed.CompareAndSwap(false, true) {
		b[0] = buttonPressed
		return len(b), nil
	}
	<-ctx.Done()
	return 0, ctx.Err()
}

func TestWatchButton_ReportsAPress(t *testing.T) {
	dev := &pressOnce{fakeDevice: &fakeDevice{t: t}}
	withFakeOpenDevice(t, dev, nil)
	d := New().(*dxDriver)

	ctx, cancel := context.WithCancel(context.Background())
	got := make(chan struct{}, 1)
	done := make(chan struct{})
	go func() {
		d.WatchButton(ctx, func() { got <- struct{}{} })
		close(done)
	}()
	select {
	case <-got:
	case <-time.After(2 * time.Second):
		t.Fatal("the press was not reported")
	}
	cancel()
	<-done
	if !dev.closed {
		t.Error("the watcher should let go of the scanner when it stops")
	}
}

func TestWatchButton_YieldsToDetectAndOpen(t *testing.T) {
	dev := &buttonDevice{fakeDevice: &fakeDevice{t: t}}
	withFakeOpenDevice(t, dev, nil)
	d := New().(*dxDriver)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.WatchButton(ctx, func() {})

	// Wait until the watcher holds the scanner.
	deadline := time.Now().Add(2 * time.Second)
	for {
		d.mu.Lock()
		held := d.watching != nil
		d.mu.Unlock()
		if held {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the watcher never took the scanner")
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Detect is answered by the watcher's open scanner, without opening again.
	opens := 0
	withFakeOpenDevice(t, nil, errors.New("must not be opened again"))
	if err := d.Detect(context.Background()); err != nil {
		t.Fatalf("Detect while watching: %v", err)
	}

	// Open takes the scanner over: the watcher's handle is closed first.
	fresh := &buttonDevice{fakeDevice: &fakeDevice{t: t}}
	openDeviceFunc = func() (scsiusb.BulkDevice, error) { opens++; return fresh, nil }
	sess, err := d.Open(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !dev.closed {
		t.Error("Open should close the watcher's handle first")
	}
	// While the scan's session is open, the watcher stays off the scanner.
	time.Sleep(buttonRetry/4 + 2*buttonSlice)
	d.mu.Lock()
	held := d.watching != nil
	d.mu.Unlock()
	if held || opens != 1 {
		t.Errorf("watcher grabbed the scanner during a scan (held=%v, opens=%d)", held, opens)
	}
	if err := sess.Close(); err != nil {
		t.Fatal(err)
	}
	if d.sessions != 0 {
		t.Errorf("sessions = %d after Close, want 0", d.sessions)
	}
}

func TestWatchButton_ScannerGoneIsRetried(t *testing.T) {
	withFakeOpenDevice(t, nil, errors.New("unplugged"))
	d := New().(*dxDriver)
	if pressed, ok := d.pollButton(context.Background()); pressed || ok {
		t.Errorf("got pressed=%v ok=%v for an unplugged scanner", pressed, ok)
	}

	// A pipe that fails mid-wait drops the handle so the next poll reopens.
	bad := &buttonDevice{fakeDevice: &fakeDevice{t: t}, err: errors.New("usb gone")}
	withFakeOpenDevice(t, bad, nil)
	if pressed, ok := d.pollButton(context.Background()); pressed || ok {
		t.Errorf("failed pipe: pressed=%v ok=%v", pressed, ok)
	}
	if d.watching != nil || !bad.closed {
		t.Error("a failed pipe should release the scanner")
	}

	// A scanner with no interrupt pipe is not worth holding.
	plain := &fakeDevice{t: t}
	withFakeOpenDevice(t, plain, nil)
	if _, ok := d.pollButton(context.Background()); ok || d.watching != nil || !plain.closed {
		t.Error("a scanner without an interrupt pipe should be let go")
	}
}
