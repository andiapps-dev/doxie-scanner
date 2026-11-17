package doxiedx400

import (
	"errors"
	"testing"
)

func TestHasPaper(t *testing.T) {
	for _, c := range []struct {
		reply []byte
		want  bool
	}{
		{[]byte{0x00, 0x00, 0x02, 0x05, 0x01}, true},
		{[]byte{0x00, 0x00, 0x02, 0x05, 0x00}, false},
	} {
		dev := &fakeDevice{t: t}
		dev.ok(c.reply)
		got, err := hasPaper(dev)
		if err != nil {
			t.Fatal(err)
		}
		if got != c.want {
			t.Errorf("reply % x: got %v, want %v", c.reply, got, c.want)
		}
		if op := dev.opcodes()[0]; op != [3]byte{opSensor, 0, 0} {
			t.Errorf("sent %v, want sensor status", op)
		}
	}
}

func TestMustSucceed_ChuteEmptyBecomesErrNoPaper(t *testing.T) {
	dev := &fakeDevice{t: t}
	dev.checkCondition(nil, senseBlock(ascADF, ascqChuteEmpty, 0, 0, 0))

	if err := control(dev, opReserve, flagScanUnit); !errors.Is(err, ErrNoPaper) {
		t.Fatalf("got %v, want ErrNoPaper", err)
	}
}

func TestCommand_PassesThroughTransportErrors(t *testing.T) {
	dev := &fakeDevice{t: t, readErr: errors.New("usb gone")}
	if _, err := hasPaper(dev); err == nil {
		t.Fatal("expected an error")
	}
}

func TestReadImageChunk_IsNotRetried(t *testing.T) {
	dev := &fakeDevice{t: t, readErr: errors.New("stall")}
	if _, _, err := readImageChunk(dev); err == nil {
		t.Fatal("expected an error")
	}
	if len(dev.commands) != 1 {
		t.Errorf("image READ sent %d times, want exactly 1", len(dev.commands))
	}
}

func TestCommand_RejectsTruncatedSense(t *testing.T) {
	dev := &fakeDevice{t: t}
	dev.checkCondition(nil, make([]byte, 10))

	err := control(dev, opReserve, flagJob)
	if err == nil || errors.Is(err, ErrNoPaper) {
		t.Fatalf("got %v, want a short-sense error", err)
	}
}
