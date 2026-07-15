package scsiusb

import (
	"bytes"
	"errors"
	"testing"
	"time"
)

const testRetryDelay = time.Millisecond // keep tests fast

func TestPadCDB(t *testing.T) {
	got := PadCDB([]byte{0x12, 0x00})
	want := []byte{0x12, 0x00, 0, 0, 0, 0, 0, 0, 0, 0}
	if !bytes.Equal(got, want) {
		t.Errorf("PadCDB short: got % x, want % x", got, want)
	}

	exact := make([]byte, MinCDBLen)
	for i := range exact {
		exact[i] = byte(i + 1)
	}
	if got := PadCDB(exact); !bytes.Equal(got, exact) {
		t.Errorf("PadCDB exact-length: got % x, want % x", got, exact)
	}

	longer := make([]byte, MinCDBLen+2)
	if got := PadCDB(longer); len(got) != len(longer) {
		t.Errorf("PadCDB longer-than-min: got len %d, want %d", len(got), len(longer))
	}
}

func TestCommand_SuccessNoData(t *testing.T) {
	dev := &fakeBulkDevice{t: t}
	dev.queueGoodStatus()

	cdb := []byte{0x16} // RESERVE_UNIT-shaped, doesn't matter here
	data, err := Command(dev, cdb, nil, 0, 4, testRetryDelay)
	if err != nil {
		t.Fatalf("Command: %v", err)
	}
	if len(data) != 0 {
		t.Errorf("expected no data, got % x", data)
	}
	if len(dev.writeLog) != 1 {
		t.Fatalf("expected 1 write (padded cdb), got %d", len(dev.writeLog))
	}
	if len(dev.writeLog[0]) != MinCDBLen {
		t.Errorf("cdb not padded: len=%d", len(dev.writeLog[0]))
	}
}

func TestCommand_SuccessWithOutDataAndInData(t *testing.T) {
	dev := &fakeBulkDevice{t: t}
	dev.queueRead([]byte{0xAA, 0xBB, 0xCC})
	dev.queueGoodStatus()

	outData := []byte{0x01, 0x02}
	data, err := Command(dev, []byte{0x24}, outData, 3, 4, testRetryDelay)
	if err != nil {
		t.Fatalf("Command: %v", err)
	}
	if !bytes.Equal(data, []byte{0xAA, 0xBB, 0xCC}) {
		t.Errorf("data: got % x", data)
	}
	if len(dev.writeLog) != 2 {
		t.Fatalf("expected 2 writes (cdb, outData), got %d", len(dev.writeLog))
	}
	if !bytes.Equal(dev.writeLog[1], outData) {
		t.Errorf("outData write: got % x, want % x", dev.writeLog[1], outData)
	}
}

func TestCommand_CheckConditionSurfacesSense(t *testing.T) {
	dev := &fakeBulkDevice{t: t}
	dev.queueGoodStatus() // no in_len read for this CDB
	// Replace the "good status" with a check-condition status instead:
	dev.reads = nil
	dev.queueRead([]byte{StatusCheckCondition})
	sense := make([]byte, senseBufferLen)
	sense[2] = 0x00 // sense_key 0
	sense[12], sense[13] = 0x80, 0x04
	dev.queueRead(sense)
	dev.queueGoodStatus() // trailing ignored status after REQUEST SENSE

	data, err := Command(dev, []byte{0x28}, nil, 0, 4, testRetryDelay)
	if data != nil {
		t.Errorf("expected nil data alongside CheckConditionError, got % x", data)
	}
	var cc *CheckConditionError
	if !errors.As(err, &cc) {
		t.Fatalf("expected *CheckConditionError, got %v (%T)", err, err)
	}
	if !bytes.Equal(cc.Sense, sense) {
		t.Errorf("sense: got % x, want % x", cc.Sense, sense)
	}
}

func TestCommand_BusyThenSuccess(t *testing.T) {
	dev := &fakeBulkDevice{t: t}
	dev.queueRead([]byte{StatusBusy})
	dev.queueGoodStatus()

	_, err := Command(dev, []byte{0x00}, nil, 0, 4, testRetryDelay)
	if err != nil {
		t.Fatalf("Command: %v", err)
	}
	// Busy triggers a full retry: cdb gets rewritten.
	if len(dev.writeLog) != 2 {
		t.Fatalf("expected 2 cdb writes (1 busy + 1 success), got %d", len(dev.writeLog))
	}
}

func TestCommand_UnexpectedStatusRetriesThenFails(t *testing.T) {
	dev := &fakeBulkDevice{t: t}
	for i := 0; i < 4; i++ {
		dev.queueRead([]byte{0x7f}) // never a valid status
	}

	_, err := Command(dev, []byte{0x12}, nil, 0, 4, testRetryDelay)
	if err == nil {
		t.Fatal("expected an error after exhausting retries")
	}
	var scsiErr *ScsiError
	if !errors.As(err, &scsiErr) {
		t.Fatalf("expected *ScsiError, got %v (%T)", err, err)
	}
	if len(dev.writeLog) != 4 {
		t.Errorf("expected 4 attempts, got %d", len(dev.writeLog))
	}
}

func TestCommand_WriteErrorRetries(t *testing.T) {
	dev := &fakeBulkDevice{t: t}
	// First Read() call ever made will error; but we want the *write* to
	// fail instead, so use a device wrapper.
	fw := &failingWriteThenGoodDevice{fake: dev, failWrites: 1}
	dev.queueGoodStatus()

	_, err := Command(fw, []byte{0x08}, nil, 0, 4, testRetryDelay)
	if err != nil {
		t.Fatalf("Command: %v", err)
	}
	if fw.writeAttempts != 2 {
		t.Errorf("expected 2 write attempts (1 fail + 1 success), got %d", fw.writeAttempts)
	}
}

// failingWriteThenGoodDevice fails the first N writes, then delegates to
// the wrapped fake for everything else.
type failingWriteThenGoodDevice struct {
	fake          *fakeBulkDevice
	failWrites    int
	writeAttempts int
}

func (f *failingWriteThenGoodDevice) Write(p []byte) (int, error) {
	f.writeAttempts++
	if f.failWrites > 0 {
		f.failWrites--
		return 0, errors.New("simulated transport write failure")
	}
	return f.fake.Write(p)
}

func (f *failingWriteThenGoodDevice) Read(p []byte) (int, error) { return f.fake.Read(p) }
func (f *failingWriteThenGoodDevice) Close() error               { return f.fake.Close() }

func TestCommand_ReadTimeoutReturnsEmptyData(t *testing.T) {
	dev := &fakeBulkDevice{t: t}
	dev.queueReadErr(ErrTimeout) // times out immediately, no data at all
	dev.queueGoodStatus()

	data, err := Command(dev, []byte{0x28}, nil, 5, 4, testRetryDelay)
	if err != nil {
		t.Fatalf("Command: %v", err)
	}
	if len(data) != 0 {
		t.Errorf("expected empty data on immediate timeout, got % x", data)
	}
}

func TestReadExact_FullReadInOneCall(t *testing.T) {
	// A BulkDevice.Read that fully satisfies the request in one call
	// completes the loop via the outer `len(buf) < length` condition,
	// without ever needing the short-packet break.
	dev := &fakeBulkDevice{t: t}
	dev.queueRead([]byte{0x01, 0x02, 0x03})

	data, err := readExact(dev, 3)
	if err != nil {
		t.Fatalf("readExact: %v", err)
	}
	if !bytes.Equal(data, []byte{0x01, 0x02, 0x03}) {
		t.Errorf("got % x", data)
	}
}

func TestReadExact_ShortPacketStopsEarly(t *testing.T) {
	dev := &fakeBulkDevice{t: t}
	dev.queueRead([]byte{0xAA}) // 1 byte when 10 were requested: short packet

	data, err := readExact(dev, 10)
	if err != nil {
		t.Fatalf("readExact: %v", err)
	}
	if !bytes.Equal(data, []byte{0xAA}) {
		t.Errorf("expected short read to stop after 1 byte, got % x", data)
	}
}

func TestRequestSense_WritesStandardCDBAndReadsSenseBuffer(t *testing.T) {
	dev := &fakeBulkDevice{t: t}
	dev.wantWrites = [][]byte{requestSenseCDB}
	sense := bytes.Repeat([]byte{0x42}, senseBufferLen)
	dev.queueRead(sense)
	dev.queueGoodStatus()

	got, err := requestSense(dev)
	if err != nil {
		t.Fatalf("requestSense: %v", err)
	}
	if !bytes.Equal(got, sense) {
		t.Errorf("sense: got % x, want % x", got, sense)
	}
}

func TestReadStatus_ReadError(t *testing.T) {
	dev := &fakeBulkDevice{t: t}
	dev.queueReadErr(errors.New("boom"))
	if _, err := readStatus(dev); err == nil {
		t.Fatal("expected error")
	}
}

func TestReadStatus_EmptyBuffer(t *testing.T) {
	dev := &fakeBulkDevice{t: t}
	dev.queueReadErr(ErrTimeout) // times out with zero bytes collected
	_, err := readStatus(dev)
	var scsiErr *ScsiError
	if !errors.As(err, &scsiErr) {
		t.Fatalf("expected *ScsiError for empty status buffer, got %v", err)
	}
}

func TestRequestSense_WriteError(t *testing.T) {
	dev := &alwaysFailWriteDevice{}
	if _, err := requestSense(dev); err == nil {
		t.Fatal("expected error")
	}
}

func TestRequestSense_ReadError(t *testing.T) {
	dev := &fakeBulkDevice{t: t}
	dev.queueReadErr(errors.New("boom"))
	if _, err := requestSense(dev); err == nil {
		t.Fatal("expected error")
	}
}

func TestCommand_OutDataWriteErrorRetries(t *testing.T) {
	dev := &fakeBulkDevice{t: t}
	fw := &failingSecondWriteDevice{fake: dev, failCount: 1}
	dev.queueGoodStatus()

	_, err := Command(fw, []byte{0x24}, []byte{0x01}, 0, 4, testRetryDelay)
	if err != nil {
		t.Fatalf("Command: %v", err)
	}
}

func TestCommand_AlwaysFailingWriteExhaustsRetries(t *testing.T) {
	dev := &alwaysFailWriteDevice{}
	_, err := Command(dev, []byte{0x12}, nil, 0, 3, testRetryDelay)
	if err == nil {
		t.Fatal("expected an error")
	}
	if dev.attempts != 3 {
		t.Errorf("expected 3 attempts, got %d", dev.attempts)
	}
}

func TestCommand_CheckConditionRequestSenseFails(t *testing.T) {
	dev := &fakeBulkDevice{t: t}
	dev.queueRead([]byte{StatusCheckCondition})
	dev.queueReadErr(errors.New("sense read failed"))
	// Retries the whole command from scratch after the failed sense fetch;
	// second attempt succeeds cleanly.
	dev.queueGoodStatus()

	_, err := Command(dev, []byte{0x28}, nil, 0, 4, testRetryDelay)
	if err != nil {
		t.Fatalf("Command: %v", err)
	}
}

// alwaysFailWriteDevice fails every Write call; Read is never expected to
// be reached through it in these tests.
type alwaysFailWriteDevice struct{ attempts int }

func (d *alwaysFailWriteDevice) Write(p []byte) (int, error) {
	d.attempts++
	return 0, errors.New("simulated permanent write failure")
}
func (d *alwaysFailWriteDevice) Read(p []byte) (int, error) { return 0, errors.New("unused") }
func (d *alwaysFailWriteDevice) Close() error               { return nil }

// failingSecondWriteDevice fails exactly the Nth Write call (1-indexed),
// then delegates to the wrapped fake.
type failingSecondWriteDevice struct {
	fake      *fakeBulkDevice
	failCount int
	calls     int
}

func (f *failingSecondWriteDevice) Write(p []byte) (int, error) {
	f.calls++
	if f.calls == 2 && f.failCount > 0 {
		f.failCount--
		return 0, errors.New("simulated outData write failure")
	}
	return f.fake.Write(p)
}
func (f *failingSecondWriteDevice) Read(p []byte) (int, error) { return f.fake.Read(p) }
func (f *failingSecondWriteDevice) Close() error               { return f.fake.Close() }

func TestReadExact_ZeroByteReadStopsLoop(t *testing.T) {
	dev := &fakeBulkDevice{t: t}
	dev.queueRead(nil) // n=0, nil error: device has nothing more, not a timeout

	data, err := readExact(dev, 5)
	if err != nil {
		t.Fatalf("readExact: %v", err)
	}
	if len(data) != 0 {
		t.Errorf("expected empty data, got % x", data)
	}
}

func TestCommand_InDataReadErrorRetries(t *testing.T) {
	dev := &fakeBulkDevice{t: t}
	dev.queueReadErr(errors.New("in-data read failed"))
	dev.queueRead([]byte{0xAA, 0xBB})
	dev.queueGoodStatus()

	data, err := Command(dev, []byte{0x28}, nil, 2, 4, testRetryDelay)
	if err != nil {
		t.Fatalf("Command: %v", err)
	}
	if !bytes.Equal(data, []byte{0xAA, 0xBB}) {
		t.Errorf("got % x", data)
	}
}

func TestCommand_StatusReadErrorRetries(t *testing.T) {
	dev := &fakeBulkDevice{t: t}
	dev.queueReadErr(errors.New("status read failed"))
	dev.queueGoodStatus()

	_, err := Command(dev, []byte{0x16}, nil, 0, 4, testRetryDelay)
	if err != nil {
		t.Fatalf("Command: %v", err)
	}
}

func TestErrString(t *testing.T) {
	if got := errString(nil); got != "no error recorded" {
		t.Errorf("errString(nil): got %q", got)
	}
	if got := errString(errors.New("boom")); got != "boom" {
		t.Errorf("errString: got %q", got)
	}
}
