package scsiusb

import (
	"bytes"
	"errors"
	"testing"
	"time"
)

const testRetryDelay = time.Millisecond

func TestReadExact(t *testing.T) {
	dev := &fakeBulkDevice{t: t}
	dev.queueRead([]byte{0x01, 0x02, 0x03})
	if got, err := readExact(dev, 3); err != nil || !bytes.Equal(got, []byte{1, 2, 3}) {
		t.Errorf("full read: got % x, %v", got, err)
	}

	dev.queueRead([]byte{0x01})
	if got, _ := readExact(dev, 5); len(got) != 1 {
		t.Errorf("short packet should end the read, got % x", got)
	}

	dev.queueRead(nil)
	if got, _ := readExact(dev, 5); len(got) != 0 {
		t.Errorf("zero-byte read should end the read, got % x", got)
	}

	dev.queueReadErr(ErrTimeout)
	if got, err := readExact(dev, 5); err != nil || len(got) != 0 {
		t.Errorf("timeout means no data, got % x, %v", got, err)
	}

	dev.queueReadErr(errors.New("boom"))
	if _, err := readExact(dev, 5); err == nil {
		t.Error("expected a real read error to surface")
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

func TestCommand12_UsesCommandSenderWhenAvailable(t *testing.T) {
	dev := &commandSenderDevice{fakeBulkDevice: &fakeBulkDevice{t: t}}
	dev.queueGoodStatus()
	window := []byte{0xAB, 0xCD}

	if _, err := Command12(dev, CDB12(0xE4, 0, 2, 0), window, 0, 1, testRetryDelay); err != nil {
		t.Fatal(err)
	}
	if len(dev.sent) != 1 || dev.sent[0][0] != 0xE4 {
		t.Errorf("CDB should go out of band, sent %v", dev.sent)
	}
	if len(dev.writeLog) != 1 || !bytes.Equal(dev.writeLog[0], window) {
		t.Errorf("only the payload should go over bulk, wrote %v", dev.writeLog)
	}
}

func TestCommand12_RetriesTransportFailures(t *testing.T) {
	cases := map[string]func(*fakeBulkDevice) BulkDevice{
		"cdb write": func(f *fakeBulkDevice) BulkDevice {
			f.queueRead([]byte{0xAA})
			f.queueGoodStatus()
			return &flakyWriteDevice{fakeBulkDevice: f, failOn: map[int]bool{1: true}}
		},
		"payload write": func(f *fakeBulkDevice) BulkDevice {
			f.queueRead([]byte{0xAA})
			f.queueGoodStatus()
			return &flakyWriteDevice{fakeBulkDevice: f, failOn: map[int]bool{2: true}}
		},
		"data read": func(f *fakeBulkDevice) BulkDevice {
			f.queueReadErr(errors.New("data read failed"))
			f.queueRead([]byte{0xAA})
			f.queueGoodStatus()
			return f
		},
		"status read": func(f *fakeBulkDevice) BulkDevice {
			f.queueRead([]byte{0xAA})
			f.queueReadErr(errors.New("status read failed"))
			f.queueRead([]byte{0xAA})
			f.queueGoodStatus()
			return f
		},
		"sense fetch": func(f *fakeBulkDevice) BulkDevice {
			f.queueRead([]byte{0xAA})
			f.queueRead([]byte{0x00, 0x00, StatusCheckCondition})
			f.queueReadErr(errors.New("sense read failed"))
			f.queueRead([]byte{0xAA})
			f.queueGoodStatus()
			return f
		},
	}
	for name, setup := range cases {
		dev := setup(&fakeBulkDevice{t: t})
		if _, err := Command12(dev, CDB12(0xAA, 0x81, 1, 0), []byte{0x01}, 1, 4, testRetryDelay); err != nil {
			t.Errorf("%s: expected a retry to recover, got %v", name, err)
		}
	}
}

func TestCommand12_ExhaustsRetries(t *testing.T) {
	dev := &flakyWriteDevice{fakeBulkDevice: &fakeBulkDevice{t: t}, failOn: map[int]bool{1: true, 2: true, 3: true}}
	if _, err := Command12(dev, CDB12(0xE2, 0, 0, 0), nil, 0, 3, testRetryDelay); err == nil {
		t.Fatal("expected an error after 3 failed attempts")
	}
	if dev.calls != 3 {
		t.Errorf("expected 3 attempts, got %d", dev.calls)
	}
}

func TestCommand12_UnexpectedStatusFails(t *testing.T) {
	dev := &fakeBulkDevice{t: t}
	dev.queueRead([]byte{0x00, 0x00, 0x7f})
	if _, err := Command12(dev, CDB12(0xE6, 0, 0, 0), nil, 0, 1, testRetryDelay); err == nil {
		t.Fatal("expected an unknown status to fail")
	}
}

func TestCommand12_RetriesWhenSenseRequestCannotBeSent(t *testing.T) {
	f := &fakeBulkDevice{t: t}
	f.queueRead([]byte{0x00, 0x00, StatusCheckCondition})
	f.queueGoodStatus()
	dev := &flakyWriteDevice{fakeBulkDevice: f, failOn: map[int]bool{2: true}}

	if _, err := Command12(dev, CDB12(0xE6, 0, 0, 0), nil, 0, 4, testRetryDelay); err != nil {
		t.Fatalf("expected a retry after the failed sense request, got %v", err)
	}
}
