package doxiedx400

import (
	"errors"
	"testing"

	"github.com/andiapps-dev/doxie-scanner/internal/scsiusb"
)

func TestClassifySense_ShortBuffer(t *testing.T) {
	err := classifySense(make([]byte, 10)) // < 14 bytes
	var scsiErr *scsiusb.ScsiError
	if !errors.As(err, &scsiErr) {
		t.Fatalf("expected *scsiusb.ScsiError, got %v (%T)", err, err)
	}
}

func TestClassifySense_KeyZeroIsNil(t *testing.T) {
	sense := make([]byte, senseBufferLen)
	sense[2] = 0x00
	if err := classifySense(sense); err != nil {
		t.Errorf("expected nil, got %v", err)
	}
}

func TestClassifySense_AdfChuteEmpty(t *testing.T) {
	sense := make([]byte, senseBufferLen)
	sense[2] = 0x02
	sense[12], sense[13] = 0x80, 0x03
	var sc *scsiusb.ScanComplete
	if err := classifySense(sense); !errors.As(err, &sc) {
		t.Fatalf("expected *scsiusb.ScanComplete, got %v (%T)", err, err)
	}
}

func TestClassifySense_AdfPaperEnd(t *testing.T) {
	sense := make([]byte, senseBufferLen)
	sense[2] = 0x02
	sense[12], sense[13] = 0x80, 0x04
	var sc *scsiusb.ScanComplete
	if err := classifySense(sense); !errors.As(err, &sc) {
		t.Fatalf("expected *scsiusb.ScanComplete, got %v (%T)", err, err)
	}
}

func TestClassifySense_RealError(t *testing.T) {
	sense := make([]byte, senseBufferLen)
	sense[2] = 0x04 // hardware error
	sense[12], sense[13] = 0x44, 0x00
	var scsiErr *scsiusb.ScsiError
	if err := classifySense(sense); !errors.As(err, &scsiErr) {
		t.Fatalf("expected *scsiusb.ScsiError, got %v (%T)", err, err)
	}
}
