package scsiusb

import (
	"errors"
	"strings"
	"testing"
)

func TestErrDeviceNotFound(t *testing.T) {
	err := &ErrDeviceNotFound{VID: 0x2740, PID: 0x000c}
	msg := err.Error()
	if !strings.Contains(msg, "2740") || !strings.Contains(msg, "000c") {
		t.Errorf("error message missing VID/PID: %q", msg)
	}
}

func TestErrClaimInterface(t *testing.T) {
	cause := errors.New("permission denied")
	err := &ErrClaimInterface{Cause: cause}
	if !strings.Contains(err.Error(), "permission denied") {
		t.Errorf("error message missing cause: %q", err.Error())
	}
	if !errors.Is(err, cause) {
		if errors.Unwrap(err) != cause {
			t.Errorf("Unwrap did not return the cause")
		}
	}
}

func TestScsiError(t *testing.T) {
	e1 := &ScsiError{Opcode: 0x28, Message: "boom"}
	if !strings.Contains(e1.Error(), "28") || !strings.Contains(e1.Error(), "boom") {
		t.Errorf("unexpected message: %q", e1.Error())
	}
	e2 := &ScsiError{Opcode: 0x12}
	if !strings.Contains(e2.Error(), "12") {
		t.Errorf("unexpected message for empty Message: %q", e2.Error())
	}
}

func TestCheckConditionError(t *testing.T) {
	err := &CheckConditionError{Sense: []byte{0x70, 0x00, 0x00}}
	if !strings.Contains(err.Error(), "70 00 00") {
		t.Errorf("unexpected message: %q", err.Error())
	}
}

func TestScanCompleteError(t *testing.T) {
	err := &ScanComplete{Reason: "ADF paper end"}
	if !strings.Contains(err.Error(), "ADF paper end") {
		t.Errorf("unexpected message: %q", err.Error())
	}
}
