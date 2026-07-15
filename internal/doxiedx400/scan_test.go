package doxiedx400

import (
	"errors"
	"testing"
)

// scriptFullPageSetup queues the reads for the entire per-page setup
// sequence (SET WINDOW through START SCAN), matching scanOnePage's exact
// call order.
func scriptFullPageSetup(dev *fakeDevice) {
	dev.queueGoodStatus()                  // SET WINDOW
	dev.queueOKCommand(make([]byte, 0x20)) // calibration format READ
	dev.queueGoodStatus()                  // color matrix SEND
	dev.queueGoodStatus()                  // gamma channel 0
	dev.queueGoodStatus()                  // gamma channel 1
	dev.queueGoodStatus()                  // gamma channel 2
	dev.queueGoodStatus()                  // attach truncate tail
	dev.queueGoodStatus()                  // attach truncate head
	dev.queueGoodStatus()                  // reserve unit
	dev.queueGoodStatus()                  // start scan
}

// TestScanOnePage_FullRealisticConversation scripts an entire scan
// exchange byte-for-byte (INQUIRY-equivalent setup through RELEASE UNIT),
// the same rigor the Python reference implementation got from real
// hardware, reproduced deterministically here.
func TestScanOnePage_FullRealisticConversation(t *testing.T) {
	dev := &fakeDevice{t: t}
	scriptFullPageSetup(dev)

	// Three image-data chunks, then a graceful end-of-document.
	chunk1 := make([]byte, readChunkBytes)
	chunk1[0] = 0x01
	chunk2 := make([]byte, readChunkBytes)
	chunk2[0] = 0x02
	chunk3 := make([]byte, readChunkBytes)
	chunk3[0] = 0x03
	dev.queueOKCommand(chunk1)
	dev.queueOKCommand(chunk2)
	dev.queueOKCommand(chunk3)
	dev.queueEndOfDocumentImageRead()

	// Cleanup: REQUEST SENSE, RELEASE UNIT x2.
	dev.queueOKCommand(make([]byte, senseBufferLen))
	dev.queueGoodStatus()
	dev.queueGoodStatus()

	chunks, err := scanOnePage(dev, false)
	if err != nil {
		t.Fatalf("scanOnePage: %v", err)
	}
	if len(chunks) != 3 {
		t.Fatalf("expected 3 chunks, got %d", len(chunks))
	}
	if chunks[0][0] != 0x01 || chunks[1][0] != 0x02 || chunks[2][0] != 0x03 {
		t.Errorf("chunks out of order or corrupted")
	}
}

func TestScanOnePage_EmptyReadEndsCleanly(t *testing.T) {
	dev := &fakeDevice{t: t}
	scriptFullPageSetup(dev)
	dev.queueOKCommand(nil) // zero-length read: also a valid "no more data" signal
	dev.queueOKCommand(make([]byte, senseBufferLen))
	dev.queueGoodStatus()
	dev.queueGoodStatus()

	chunks, err := scanOnePage(dev, false)
	if err != nil {
		t.Fatalf("scanOnePage: %v", err)
	}
	if len(chunks) != 0 {
		t.Errorf("expected 0 chunks, got %d", len(chunks))
	}
}

func TestScanOnePage_DuplexUsesduplexWindow(t *testing.T) {
	dev := &fakeDevice{t: t}
	dev.wantWrites = [][]byte{nil, windowDataDuplex} // first write is the CDB, don't care about its exact bytes here
	scriptFullPageSetup(dev)
	dev.queueEndOfDocumentImageRead()
	dev.queueOKCommand(make([]byte, senseBufferLen))
	dev.queueGoodStatus()
	dev.queueGoodStatus()

	if _, err := scanOnePage(dev, true); err != nil {
		t.Fatalf("scanOnePage(duplex): %v", err)
	}
}

func TestScanOnePage_RealErrorDuringReadLoopSkipsCleanup(t *testing.T) {
	dev := &fakeDevice{t: t}
	scriptFullPageSetup(dev)
	// A real (non-end-of-document) error during the image read loop.
	dev.queueRead([]byte{0x02}) // check condition
	sense := make([]byte, senseBufferLen)
	sense[2] = 0x04 // hardware error
	sense[12], sense[13] = 0x44, 0x00
	dev.queueRead(sense)
	dev.queueGoodStatus()

	_, err := scanOnePage(dev, false)
	if err == nil {
		t.Fatal("expected an error")
	}
	// No REQUEST SENSE/RELEASE UNIT reads should have been consumed since
	// scanOnePage must return immediately without attempting cleanup.
	if len(dev.reads) != 0 {
		t.Errorf("expected no leftover scripted reads (cleanup should have been skipped), got %d remaining", len(dev.reads))
	}
}

func TestScanOnePage_SetupErrorPropagatesImmediately(t *testing.T) {
	dev := &alwaysFailDevice{}
	_, err := scanOnePage(dev, false)
	if err == nil {
		t.Fatal("expected an error")
	}
}

// failFromWriteDevice wraps a fakeDevice and fails every Write() call
// from the Nth one (1-indexed) onward, delegating everything before that
// to the wrapped fake. Since a failed write means Command() never
// reaches the read phase for that step, the wrapped fake only needs
// reads queued for the steps that succeed before the failure point;
// extra unused queued reads are harmless.
type failFromWriteDevice struct {
	fake     *fakeDevice
	failFrom int
	writeNum int
}

func (d *failFromWriteDevice) Write(p []byte) (int, error) {
	d.writeNum++
	if d.writeNum >= d.failFrom {
		return 0, errors.New("simulated write failure")
	}
	return d.fake.Write(p)
}
func (d *failFromWriteDevice) Read(p []byte) (int, error) { return d.fake.Read(p) }
func (d *failFromWriteDevice) Close() error               { return d.fake.Close() }

// Write-call counts for a fully successful per-page setup phase (see
// scriptFullPageSetup): setWindow(2) + readCalibrationFormat(1) +
// sendColorMatrix(2) + 3x sendGammaChannel(2 each=6) +
// 2x sendAttachTruncate(2 each=4) + reserveUnit(1) + startScan(1) = 17.
const (
	writeNumReadCalibrationFormat = 3
	writeNumSendColorMatrix       = 4
	writeNumGammaChannel0         = 6
	writeNumAttachTail            = 12
	writeNumAttachHead            = 14
	writeNumReserveUnit           = 16
	writeNumStartScan             = 17
)

func TestScanOnePage_EachSetupStepErrorPropagates(t *testing.T) {
	cases := map[string]int{
		"readCalibrationFormat":  writeNumReadCalibrationFormat,
		"sendColorMatrix":        writeNumSendColorMatrix,
		"sendGammaChannel":       writeNumGammaChannel0,
		"sendAttachTruncateTail": writeNumAttachTail,
		"sendAttachTruncateHead": writeNumAttachHead,
		"reserveUnit":            writeNumReserveUnit,
		"startScan":              writeNumStartScan,
	}
	for name, failAt := range cases {
		t.Run(name, func(t *testing.T) {
			fake := &fakeDevice{t: t}
			scriptFullPageSetup(fake)
			dev := &failFromWriteDevice{fake: fake, failFrom: failAt}

			_, err := scanOnePage(dev, false)
			if err == nil {
				t.Fatalf("expected an error when %s fails", name)
			}
		})
	}
}

func TestScanOnePage_ReleaseUnitErrorsPropagate(t *testing.T) {
	// writeNumStartScan(17) succeeds, then: write #18 is the final
	// (end-of-document) image READ cdb; hitting check-condition makes
	// scsiusb.Command itself issue an *automatic* internal REQUEST SENSE
	// write (#19) before ever returning to scanOnePage. scanOnePage then
	// issues its own separate, explicit REQUEST SENSE (#20, matching the
	// reference implementation's unconditional post-loop call), then
	// release unit 0x00 (#21) and release unit 0x01 (#22).
	cases := map[string]int{
		"releaseUnit0x00": 21,
		"releaseUnit0x01": 22,
	}
	for name, failAt := range cases {
		t.Run(name, func(t *testing.T) {
			fake := &fakeDevice{t: t}
			scriptFullPageSetup(fake)
			fake.queueEndOfDocumentImageRead()
			fake.queueOKCommand(make([]byte, senseBufferLen)) // REQUEST SENSE
			fake.queueGoodStatus()                            // release unit 0x00 (unused if that write itself fails)
			dev := &failFromWriteDevice{fake: fake, failFrom: failAt}

			_, err := scanOnePage(dev, false)
			if err == nil {
				t.Fatalf("expected an error when %s fails", name)
			}
		})
	}
}

func TestScanOnePage_CleanupErrorPropagates(t *testing.T) {
	dev := &fakeDevice{t: t}
	scriptFullPageSetup(dev)
	dev.queueEndOfDocumentImageRead()
	// REQUEST SENSE fails outright.
	dev.reads = append(dev.reads, fakeRead{err: errors.New("boom")})

	_, err := scanOnePage(dev, false)
	if err == nil {
		t.Fatal("expected an error from the cleanup phase")
	}
}
