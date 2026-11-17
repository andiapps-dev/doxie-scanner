package scsiusb

import (
	"bytes"
	"errors"
	"fmt"
	"testing"
)

var good12 = []byte{0x00, 0x00, 0x00}

func TestCDB12Layout(t *testing.T) {
	cases := []struct {
		name string
		got  []byte
		want string
	}{
		{"inquiry header", CDB12(0xE2, 0, 5, 0), "e2 00 00 00 00 00 00 00 00 00 05 00"},
		{"nvm read", CDB12(0xA8, 0x69, 0x84, 0), "a8 00 00 00 69 00 00 00 00 00 84 00"},
		{"image read", CDB12(0xA8, 0x00, 0x40000, 0), "a8 00 00 00 00 00 00 00 04 00 00 00"},
		{"gamma send", CDB12(0xAA, 0x81, 0x208, 0), "aa 00 00 00 81 00 00 00 00 02 08 00"},
		{"end job", CDB12(0xE7, 0, 0, 0x02), "e7 00 00 00 00 00 00 00 00 00 00 02"},
	}
	for _, c := range cases {
		if got := hexString(c.got); got != c.want {
			t.Errorf("%s: got %s, want %s", c.name, got, c.want)
		}
	}
}

func TestCDB12PanicsOnOversizeLength(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for a length that doesn't fit in 24 bits")
		}
	}()
	CDB12(0xA8, 0, 1<<24, 0)
}

func TestCommand12_ReadsDataThenStatus(t *testing.T) {
	dev := &fakeBulkDevice{t: t}
	dev.queueRead([]byte{0x00, 0x00, 0x02, 0x00, 0xcb})
	dev.queueRead(good12)

	data, err := Command12(dev, CDB12(0xE2, 0, 5, 0), nil, 5, 4, testRetryDelay)
	if err != nil {
		t.Fatalf("Command12: %v", err)
	}
	if !bytes.Equal(data, []byte{0x00, 0x00, 0x02, 0x00, 0xcb}) {
		t.Errorf("data: got % x", data)
	}
	if len(dev.writeLog) != 1 || len(dev.writeLog[0]) != CDB12Len {
		t.Errorf("expected one 12-byte CDB write, got %d writes", len(dev.writeLog))
	}
}

func TestCommand12_WritesOutData(t *testing.T) {
	dev := &fakeBulkDevice{t: t}
	dev.queueRead(good12)
	window := bytes.Repeat([]byte{0xAB}, 0x60)

	if _, err := Command12(dev, CDB12(0xE4, 0, len(window), 0), window, 0, 4, testRetryDelay); err != nil {
		t.Fatalf("Command12: %v", err)
	}
	if len(dev.writeLog) != 2 || !bytes.Equal(dev.writeLog[1], window) {
		t.Fatalf("expected CDB then window payload, got %d writes", len(dev.writeLog))
	}
}

func TestCommand12_CheckConditionFetchesVendorSense(t *testing.T) {
	sense := make([]byte, Sense12Len)
	copy(sense, []byte{0x00, 0x00, 0x02, 0x2c, 0xf0, 0x03, 0x25, 0x80, 0x04})

	dev := &fakeBulkDevice{t: t}
	dev.queueRead(make([]byte, 64))
	dev.queueRead([]byte{0x00, 0x00, StatusCheckCondition})
	dev.queueRead(sense)
	dev.queueRead(good12)

	data, err := Command12(dev, CDB12(0xA8, 0, 64, 0), nil, 64, 1, testRetryDelay)
	var cc *CheckConditionError
	if !errors.As(err, &cc) {
		t.Fatalf("expected CheckConditionError, got %v", err)
	}
	if !bytes.Equal(cc.Sense, sense) {
		t.Errorf("sense: got % x", cc.Sense)
	}
	if len(data) != 64 {
		t.Errorf("data read before the check condition should be returned, got %d bytes", len(data))
	}
	if got := hexString(dev.writeLog[1]); got != "e3 00 00 00 00 00 00 00 00 00 2c 00" {
		t.Errorf("sense CDB: got %s", got)
	}
}

func TestCommand12_RetriesBusyThenSucceeds(t *testing.T) {
	dev := &fakeBulkDevice{t: t}
	dev.queueRead([]byte{0x00, 0x00, StatusBusy})
	dev.queueRead(good12)

	if _, err := Command12(dev, CDB12(0xE6, 0, 0, 0), nil, 0, 4, testRetryDelay); err != nil {
		t.Fatalf("Command12: %v", err)
	}
	if len(dev.writeLog) != 2 {
		t.Errorf("expected the CDB to be sent twice, got %d writes", len(dev.writeLog))
	}
}

func TestCommand12_ShortStatusFails(t *testing.T) {
	dev := &fakeBulkDevice{t: t}
	dev.queueRead([]byte{0x00})

	if _, err := Command12(dev, CDB12(0xE6, 0, 0, 0), nil, 0, 1, testRetryDelay); err == nil {
		t.Fatal("expected an error for a 1-byte status")
	}
}

func TestCommand12_RejectsWrongCDBLength(t *testing.T) {
	dev := &fakeBulkDevice{t: t}
	if _, err := Command12(dev, []byte{0xE2}, nil, 0, 1, testRetryDelay); err == nil {
		t.Fatal("expected an error for a 1-byte CDB")
	}
	if len(dev.writeLog) != 0 {
		t.Errorf("nothing should be written for an invalid CDB, got %d writes", len(dev.writeLog))
	}
}

func hexString(b []byte) string { return fmt.Sprintf("% x", b) }
