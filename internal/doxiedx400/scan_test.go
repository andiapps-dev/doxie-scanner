package doxiedx400

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/andiapps-dev/doxie-scanner/internal/driver"
)

func filled(b byte, n int) []byte { return bytes.Repeat([]byte{b}, n) }

// testSession opens a session on dev whose waits only count.
func testSession(t *testing.T, dev *fakeDevice) (*session, *int) {
	t.Helper()
	s := openFake(t, dev)
	sleeps := 0
	s.sleep = func(time.Duration) { sleeps++ }
	return s, &sleeps
}

func TestStack_FirstSheetSequenceAndPayloads(t *testing.T) {
	w, _ := newWindow(300, true)
	quant := quantTables[300]
	dev := &fakeDevice{t: t}
	dev.scriptFirstSheet(true, scriptedSide{filled(0x11, 100), 10}, scriptedSide{filled(0x22, 80), 10})
	dev.ok(nil) // E7 job
	sess, _ := testSession(t, dev)
	ctx := context.Background()

	if _, err := sess.scanSides(ctx, driver.ScanOptions{Duplex: true}); err != nil {
		t.Fatal(err)
	}
	if err := sess.EndBatch(ctx); err != nil {
		t.Fatal(err)
	}

	want := [][3]byte{
		{opReserve, 0, flagJob},
		{opRead, dtScanStatus, 0},
		{opSetWindow, 0, 0},
		{opRead, dtDuplexTransfer, 0},
		{opSend, dtTruncateHead, 0},
		{opSend, dtTruncateTail, 0},
		{opSend, dtJPEGQuant, 0},
		{opRead, dtCalibrationFmt, 0},
		{opSend, dtGamma, 0}, {opSend, dtGamma, 0}, {opSend, dtGamma, 0},
		{opReserve, 0, flagScanUnit},
		{opRead, dtImageData, 0}, {opSense, 0, 0},
		{opRead, dtImageData, 0}, {opSense, 0, 0},
		{opRelease, 0, flagScanUnit},
		{opRelease, 0, flagJob},
	}
	if got := dev.opcodes(); !reflect.DeepEqual(got, want) {
		t.Fatalf("command sequence:\n got  %x\n want %x", got, want)
	}

	wantPayloads := [][]byte{
		w.bytes(), truncateNone, truncateNone, quantPayload(quant),
		gammaPayload(0), gammaPayload(1), gammaPayload(2),
	}
	if !reflect.DeepEqual(dev.payloads, wantPayloads) {
		t.Errorf("payloads differ from the captured sequence")
	}
	if len(dev.payloads[3]) != 132 || len(dev.payloads[4]) != 520 {
		t.Errorf("quant/gamma payload sizes: %d/%d, want 132/520", len(dev.payloads[3]), len(dev.payloads[4]))
	}
}

// Captured from Doxie's Windows driver scanning a four-sheet stack: the
// job stays reserved for the whole stack, later sheets repeat only the
// window, truncation and calibration steps, and the job is released once
// the feeder is empty.
func TestStack_KeepsTheJobAcrossSheetsLikeDoxiesDriver(t *testing.T) {
	side := scriptedSide{filled(0x11, 10), 1}
	dev := &fakeDevice{t: t}
	dev.scriptFirstSheet(true, side, side)
	dev.paper(true)
	dev.scriptNextSheet(side, side)
	dev.paper(true)
	dev.scriptNextSheet(side, side)
	for i := 0; i <= int(nextSheetWait/nextSheetPoll); i++ {
		dev.paper(false)
	}
	dev.ok(nil) // E7 job
	sess, _ := testSession(t, dev)
	ctx := context.Background()
	opts := driver.ScanOptions{Duplex: true}

	sheets := 0
	for {
		if _, err := sess.scanSides(ctx, opts); err != nil {
			t.Fatalf("sheet %d: %v", sheets+1, err)
		}
		sheets++
		more, err := sess.HasNextPage(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !more {
			break
		}
	}
	if err := sess.EndBatch(ctx); err != nil {
		t.Fatal(err)
	}

	if sheets != 3 || len(dev.reads) != 0 {
		t.Fatalf("scanned %d sheets with %d replies unused", sheets, len(dev.reads))
	}
	sheet := [][3]byte{
		{opReserve, 0, flagScanUnit},
		{opRead, dtImageData, 0}, {opSense, 0, 0},
		{opRead, dtImageData, 0}, {opSense, 0, 0},
		{opRelease, 0, flagScanUnit},
	}
	next := append([][3]byte{
		{opSensor, 0, 0},
		{opSetWindow, 0, 0},
		{opSend, dtTruncateHead, 0},
		{opSend, dtTruncateTail, 0},
		{opRead, dtCalibrationFmt, 0},
	}, sheet...)
	ops := dev.opcodes()
	firstEnd := 11 + len(sheet)
	if got := ops[firstEnd : firstEnd+2*len(next)]; !reflect.DeepEqual(got, append(append([][3]byte{}, next...), next...)) {
		t.Errorf("later sheets:\n got  %x\n want %x twice", got, next)
	}
	for i, op := range ops[:len(ops)-1] {
		if op == [3]byte{opRelease, 0, flagJob} || (i > 0 && op == [3]byte{opReserve, 0, flagJob}) {
			t.Errorf("command %d %x: the job must stay reserved until the stack ends", i, op)
		}
	}
	if last := ops[len(ops)-1]; last != [3]byte{opRelease, 0, flagJob} {
		t.Errorf("last command %x, want the job released", last)
	}
}

func TestStack_SimplexSkipsDuplexQuery(t *testing.T) {
	dev := &fakeDevice{t: t}
	dev.scriptFirstSheet(false, scriptedSide{filled(0x11, 50), 10})
	sess, _ := testSession(t, dev)

	page, err := sess.scanSides(context.Background(), driver.ScanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if page.Back != nil {
		t.Fatal("simplex scan should have no back")
	}
	for _, op := range dev.opcodes() {
		if op == [3]byte{opRead, dtDuplexTransfer, 0} {
			t.Error("simplex scan must not query the duplex transfer mode")
		}
	}
}

func TestReadSides_DemuxesAlternatingChunks(t *testing.T) {
	front1, rear := filled(0x11, readChunkBytes), filled(0x22, readChunkBytes)
	front2 := filled(0x33, readChunkBytes)

	dev := &fakeDevice{t: t}
	dev.ok(front1)
	dev.checkCondition(rear, senseBlock(ascADF, ascqPaperEnd, 100, 7, 100))
	dev.checkCondition(front2, senseBlock(ascADF, ascqPaperEnd, 50, 9, readChunkBytes+50))

	data, lines, err := readSides(dev, true)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data[0], append(front1, front2[:50]...)) {
		t.Error("front should be chunk 1 plus the valid part of chunk 3")
	}
	if !bytes.Equal(data[1], rear[:100]) {
		t.Error("rear should be only the valid part of chunk 2")
	}
	if lines[0] != 9 || lines[1] != 7 {
		t.Errorf("lines: got %v", lines)
	}
}

func TestReadSides_RejectsLengthMismatch(t *testing.T) {
	dev := &fakeDevice{t: t}
	dev.checkCondition(filled(0x11, 10), senseBlock(ascADF, ascqPaperEnd, 10, 1, 999))

	if _, _, err := readSides(dev, false); err == nil {
		t.Fatal("expected an error when the side length disagrees with the scanner")
	}
}

func TestReadSides_UnexpectedSenseFails(t *testing.T) {
	dev := &fakeDevice{t: t}
	dev.checkCondition([]byte{}, senseBlock(ascADF, ascqChuteEmpty, 0, 0, 0))

	if _, _, err := readSides(dev, false); !errors.Is(err, ErrNoPaper) {
		t.Fatalf("got %v, want ErrNoPaper", err)
	}
}

func TestReadSides_GivesUpWithoutEndOfPage(t *testing.T) {
	dev := &fakeDevice{t: t}
	for i := 0; i < maxChunksPerSheet; i++ {
		dev.ok([]byte{0x00})
	}
	if _, _, err := readSides(dev, false); err == nil {
		t.Fatal("expected an error after maxChunksPerSheet chunks")
	}
}

func TestStack_EndsJobAfterSetupFailure(t *testing.T) {
	dev := &fakeDevice{t: t}
	dev.ok(nil)                                           // E6 job
	dev.ok(make([]byte, 5))                               // A8 A0
	dev.checkCondition(nil, senseBlock(0x24, 0, 0, 0, 0)) // E4 rejected
	dev.ok(nil)                                           // E7 job
	sess, _ := testSession(t, dev)

	if _, err := sess.scanSides(context.Background(), driver.ScanOptions{Duplex: true}); err == nil {
		t.Fatal("expected the window rejection to surface")
	}
	ops := dev.opcodes()
	if last := ops[len(ops)-1]; last != [3]byte{opRelease, 0, flagJob} || sess.batch != nil {
		t.Errorf("last command %x, want end-of-job release", last)
	}
}

func TestStack_LaterSheetSetupFailureEndsTheJob(t *testing.T) {
	steps := []int{0, 0, 0, calibrationFmtLen} // E4, AA 95, AA 96, A8 60
	for fail := range steps {
		dev := &fakeDevice{t: t}
		dev.scriptFirstSheet(false, scriptedSide{filled(0x11, 10), 1})
		for _, n := range steps[:fail] {
			if n > 0 {
				dev.ok(make([]byte, n))
			} else {
				dev.ok(nil)
			}
		}
		var data []byte
		if steps[fail] > 0 {
			data = []byte{}
		}
		dev.checkCondition(data, senseBlock(0x24, 0, 0, 0, 0))
		dev.ok(nil) // E7 job
		sess, _ := testSession(t, dev)
		ctx := context.Background()

		if _, err := sess.scanSides(ctx, driver.ScanOptions{}); err != nil {
			t.Fatal(err)
		}
		if _, err := sess.scanSides(ctx, driver.ScanOptions{}); err == nil {
			t.Errorf("step %d: expected the rejection to surface", fail)
		}
		if ops := dev.opcodes(); ops[len(ops)-1] != [3]byte{opRelease, 0, flagJob} || len(dev.reads) != 0 {
			t.Errorf("step %d: last command %x with %d replies unused; want the job released", fail, ops[len(ops)-1], len(dev.reads))
		}
	}
}

func TestStack_ReleasesScanUnitAndJobAfterReadFailure(t *testing.T) {
	dev := &fakeDevice{t: t}
	dev.scriptFirstSheet(false)
	dev.reads = dev.reads[:len(dev.reads)-1] // drop the E7 unit reply
	dev.checkCondition([]byte{}, senseBlock(0x3b, 0, 0, 0, 0))
	dev.ok(nil) // E7 unit
	dev.ok(nil) // E7 job
	sess, _ := testSession(t, dev)

	if _, err := sess.scanSides(context.Background(), driver.ScanOptions{}); err == nil {
		t.Fatal("expected the read failure to surface")
	}
	ops := dev.opcodes()
	if n := len(ops); ops[n-2] != [3]byte{opRelease, 0, flagScanUnit} || ops[n-1] != [3]byte{opRelease, 0, flagJob} {
		t.Errorf("expected scan-unit then job release, got %x", ops[n-2:])
	}
}

func TestPrepare_StopsAtFirstRejectedStep(t *testing.T) {
	w, _ := newWindow(300, true)
	// Reply length for each step of prepare in duplex order; 0 = no data.
	steps := []int{scanStatusLen, 0, duplexTransferLen, 0, 0, 0, calibrationFmtLen, 0, 0, 0}
	for fail := range steps {
		dev := &fakeDevice{t: t}
		for _, n := range steps[:fail] {
			if n > 0 {
				dev.ok(make([]byte, n))
			} else {
				dev.ok(nil)
			}
		}
		var data []byte
		if steps[fail] > 0 {
			data = []byte{}
		}
		dev.checkCondition(data, senseBlock(0x24, 0, 0, 0, 0))

		if err := prepare(dev, w, quantTables[300]); err == nil {
			t.Errorf("step %d: expected the rejection to surface", fail)
		}
		if got := len(dev.commands); got != fail+2 {
			t.Errorf("step %d: sent %d commands, want %d (stop right after the failure)", fail, got, fail+2)
		}
	}
}

func TestStack_JobReserveFailureSendsNothingElse(t *testing.T) {
	dev := &fakeDevice{t: t}
	dev.checkCondition(nil, senseBlock(ascADF, ascqChuteEmpty, 0, 0, 0))
	sess, _ := testSession(t, dev)

	if _, err := sess.scanSides(context.Background(), driver.ScanOptions{}); !errors.Is(err, ErrNoPaper) {
		t.Fatalf("got %v, want ErrNoPaper", err)
	}
	if len(dev.commands) != 2 {
		t.Errorf("sent %d commands, want just the reserve and its sense", len(dev.commands))
	}
}

func TestStack_ReleaseFailuresSurface(t *testing.T) {
	side := scriptedSide{filled(0x11, 10), 1}
	rejected := senseBlock(0x2c, 0, 0, 0, 0)

	cases := map[string]func(*fakeDevice){
		"scan unit reserve": func(dev *fakeDevice) {
			dev.scriptFirstSheet(false)
			dev.reads = dev.reads[:len(dev.reads)-2] // drop E6 unit, E7 unit replies
			dev.checkCondition(nil, rejected)
			dev.ok(nil) // E7 job
		},
		"scan unit release": func(dev *fakeDevice) {
			dev.scriptFirstSheet(false, side)
			dev.reads = dev.reads[:len(dev.reads)-1]
			dev.checkCondition(nil, rejected)
			dev.ok(nil) // E7 job
		},
	}
	for name, script := range cases {
		dev := &fakeDevice{t: t}
		script(dev)
		sess, _ := testSession(t, dev)
		if page, err := sess.scanSides(context.Background(), driver.ScanOptions{}); err == nil || page.Front.JPEG != nil {
			t.Errorf("%s: expected an error and no page, got %v", name, err)
		}
		if len(dev.reads) != 0 {
			t.Errorf("%s: %d scripted replies unused, so the failure hit the wrong step", name, len(dev.reads))
		}
	}

	dev := &fakeDevice{t: t}
	dev.scriptFirstSheet(false, side)
	dev.checkCondition(nil, rejected) // E7 job
	sess, _ := testSession(t, dev)
	if _, err := sess.scanSides(context.Background(), driver.ScanOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := sess.EndBatch(context.Background()); err == nil {
		t.Error("job release: expected the rejection to surface")
	}
}
