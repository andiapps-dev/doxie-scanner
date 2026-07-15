package doxiedx400

import (
	"bytes"
	"encoding/hex"
	"errors"
	"testing"

	"github.com/andiapps-dev/doxie-scanner/internal/scsiusb"
)

// hexCDB decodes a hex string into a CDB byte slice, for compact
// expected-value literals matching how these were originally documented
// (e.g. "12000000600000000000" for INQUIRY) during protocol
// reverse-engineering.
func hexCDB(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("bad hex literal %q: %v", s, err)
	}
	return b
}

func TestInquiry_ExactCDB(t *testing.T) {
	dev := &fakeDevice{t: t}
	dev.wantWrites = [][]byte{hexCDB(t, "12000000600000000000")}
	dev.queueOKCommand(make([]byte, 0x60))

	if _, err := inquiry(dev); err != nil {
		t.Fatalf("inquiry: %v", err)
	}
}

func TestMediaCheck_ExactCDB(t *testing.T) {
	dev := &fakeDevice{t: t}
	dev.wantWrites = [][]byte{hexCDB(t, "08000000010000000000")}
	dev.queueOKCommand([]byte{0x01})

	data, err := mediaCheck(dev)
	if err != nil {
		t.Fatalf("mediaCheck: %v", err)
	}
	if !bytes.Equal(data, []byte{0x01}) {
		t.Errorf("got % x", data)
	}
}

func TestHasPaper(t *testing.T) {
	cases := []struct {
		byte byte
		want bool
	}{
		{0x01, true},
		{0x00, false},
		{0x03, true}, // any odd value: bit 0 set
		{0x02, false},
	}
	for _, c := range cases {
		dev := &fakeDevice{t: t}
		dev.queueOKCommand([]byte{c.byte})
		got, err := hasPaper(dev)
		if err != nil {
			t.Fatalf("hasPaper: %v", err)
		}
		if got != c.want {
			t.Errorf("hasPaper(%#02x) = %v, want %v", c.byte, got, c.want)
		}
	}
}

func TestSetWindow_SimplexExactCDB(t *testing.T) {
	dev := &fakeDevice{t: t}
	dev.wantWrites = [][]byte{
		hexCDB(t, "24000000000000004600"),
		windowData,
	}
	dev.queueGoodStatus()

	if err := setWindow(dev, false); err != nil {
		t.Fatalf("setWindow: %v", err)
	}
}

func TestSetWindow_DuplexExactCDBAndPayload(t *testing.T) {
	dev := &fakeDevice{t: t}
	dev.wantWrites = [][]byte{
		hexCDB(t, "24000000000000004600"), // same CDB (same payload length, 70 bytes)
		windowDataDuplex,
	}
	dev.queueGoodStatus()

	if err := setWindow(dev, true); err != nil {
		t.Fatalf("setWindow(duplex): %v", err)
	}
}

func TestReadCalibrationFormat_ExactCDB(t *testing.T) {
	dev := &fakeDevice{t: t}
	dev.wantWrites = [][]byte{hexCDB(t, "280060000a0d00002000")}
	dev.queueOKCommand(make([]byte, 0x20))

	if _, err := readCalibrationFormat(dev); err != nil {
		t.Fatalf("readCalibrationFormat: %v", err)
	}
}

func TestSendColorMatrix_ExactCDB(t *testing.T) {
	dev := &fakeDevice{t: t}
	dev.wantWrites = [][]byte{
		hexCDB(t, "2a008300000000001200"),
		colorMatrix,
	}
	dev.queueGoodStatus()

	if err := sendColorMatrix(dev); err != nil {
		t.Fatalf("sendColorMatrix: %v", err)
	}
}

func TestSendGammaChannel_ExactCDBPerChannel(t *testing.T) {
	want := map[byte]string{
		0: "2a008100000000020000",
		1: "2a008100000100020000",
		2: "2a008100000200020000",
	}
	for channel, hexStr := range want {
		dev := &fakeDevice{t: t}
		dev.wantWrites = [][]byte{hexCDB(t, hexStr), gammaTable}
		dev.queueGoodStatus()

		if err := sendGammaChannel(dev, channel); err != nil {
			t.Fatalf("sendGammaChannel(%d): %v", channel, err)
		}
	}
}

func TestSendAttachTruncate_TailAndHead(t *testing.T) {
	cases := map[byte]string{
		datatypeAttachTruncTail: "2a009600000100000200",
		datatypeAttachTruncHead: "2a009500000100000200",
	}
	for code, hexStr := range cases {
		dev := &fakeDevice{t: t}
		dev.wantWrites = [][]byte{hexCDB(t, hexStr), {0x00, 0x00}}
		dev.queueGoodStatus()

		if err := sendAttachTruncate(dev, code); err != nil {
			t.Fatalf("sendAttachTruncate(%#02x): %v", code, err)
		}
	}
}

func TestReserveUnit_ExactCDB(t *testing.T) {
	dev := &fakeDevice{t: t}
	dev.wantWrites = [][]byte{hexCDB(t, "16000000000000000000")}
	dev.queueGoodStatus()

	if err := reserveUnit(dev); err != nil {
		t.Fatalf("reserveUnit: %v", err)
	}
}

func TestReleaseUnit_ExactCDBBothVariants(t *testing.T) {
	cases := map[byte]string{
		0x00: "17000000000000000000",
		0x01: "17000000000100000000",
	}
	for which, hexStr := range cases {
		dev := &fakeDevice{t: t}
		dev.wantWrites = [][]byte{hexCDB(t, hexStr)}
		dev.queueGoodStatus()

		if err := releaseUnit(dev, which); err != nil {
			t.Fatalf("releaseUnit(%#02x): %v", which, err)
		}
	}
}

func TestStartScan_ExactCDB(t *testing.T) {
	dev := &fakeDevice{t: t}
	dev.wantWrites = [][]byte{hexCDB(t, "1b000000018000000000")}
	dev.queueGoodStatus()

	if err := startScan(dev); err != nil {
		t.Fatalf("startScan: %v", err)
	}
}

func TestRequestSenseCmd_ExactCDB(t *testing.T) {
	dev := &fakeDevice{t: t}
	dev.wantWrites = [][]byte{hexCDB(t, "03000000160000000000")}
	dev.queueOKCommand(make([]byte, senseBufferLen))

	if _, err := requestSenseCmd(dev); err != nil {
		t.Fatalf("requestSenseCmd: %v", err)
	}
}

func TestReadImageChunk_ExactCDB(t *testing.T) {
	dev := &fakeDevice{t: t}
	dev.wantWrites = [][]byte{hexCDB(t, "280000000a0d01dd0000")}
	dev.queueOKCommand(make([]byte, readChunkBytes))

	data, err := readImageChunk(dev)
	if err != nil {
		t.Fatalf("readImageChunk: %v", err)
	}
	if len(data) != readChunkBytes {
		t.Errorf("got %d bytes, want %d", len(data), readChunkBytes)
	}
}

func TestCommand_ClassifiesSenseKeyZeroAsSuccess(t *testing.T) {
	dev := &fakeDevice{t: t}
	dev.queueRead([]byte{0x02}) // check condition
	sense := make([]byte, senseBufferLen)
	sense[2] = 0x00 // sense_key 0: "carry on, data is valid"
	dev.queueRead(sense)
	dev.queueGoodStatus()

	data, err := command(dev, []byte{opInquiry}, nil, 0)
	if err != nil {
		t.Fatalf("command: %v", err)
	}
	_ = data
}

func TestCommand_ClassifiesEndOfDocumentAsScanComplete(t *testing.T) {
	dev := &fakeDevice{t: t}
	dev.queueCheckConditionNoData(0x05, 0x80, 0x04)

	_, err := command(dev, []byte{opRead}, nil, 0)
	var sc *scsiusb.ScanComplete
	if !errors.As(err, &sc) {
		t.Fatalf("expected *scsiusb.ScanComplete, got %v (%T)", err, err)
	}
}

func TestCommand_ClassifiesRealErrorAsScsiError(t *testing.T) {
	dev := &fakeDevice{t: t}
	dev.queueRead([]byte{0x02})
	sense := make([]byte, senseBufferLen)
	sense[2] = 0x04 // "HARDWARE ERROR"
	sense[12], sense[13] = 0x44, 0x00
	dev.queueRead(sense)
	dev.queueGoodStatus()

	_, err := command(dev, []byte{opRead}, nil, 0)
	var scsiErr *scsiusb.ScsiError
	if !errors.As(err, &scsiErr) {
		t.Fatalf("expected *scsiusb.ScsiError, got %v (%T)", err, err)
	}
}

func TestCommand_NonCheckConditionErrorPassesThrough(t *testing.T) {
	dev := &alwaysFailDevice{}
	_, err := command(dev, []byte{opInquiry}, nil, 0)
	if err == nil {
		t.Fatal("expected an error")
	}
}

func TestHasPaper_MediaCheckErrorPropagates(t *testing.T) {
	dev := &alwaysFailDevice{}
	if _, err := hasPaper(dev); err == nil {
		t.Fatal("expected an error")
	}
}

type alwaysFailDevice struct{}

func (d *alwaysFailDevice) Write(p []byte) (int, error) { return 0, errors.New("simulated failure") }
func (d *alwaysFailDevice) Read(p []byte) (int, error)  { return 0, errors.New("simulated failure") }
func (d *alwaysFailDevice) Close() error                { return nil }
