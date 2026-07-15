package scsiusb

import "fmt"

// ErrDeviceNotFound means no USB device matched the requested VID/PID.
// This is an expected, ordinary condition (the scanner may simply be
// powered off or unplugged), not a transport failure.
type ErrDeviceNotFound struct {
	VID, PID uint16
}

func (e *ErrDeviceNotFound) Error() string {
	return fmt.Sprintf("no USB device with VID %#04x PID %#04x found — is it powered on and connected?", e.VID, e.PID)
}

// ErrClaimInterface means the device was found but its USB interface
// could not be claimed (permissions, or another process already has it
// open).
type ErrClaimInterface struct {
	Cause error
}

func (e *ErrClaimInterface) Error() string {
	return fmt.Sprintf("found the device but couldn't claim the interface — check permissions/udev rules, or that no other process has it open: %v", e.Cause)
}

func (e *ErrClaimInterface) Unwrap() error { return e.Cause }

// ScsiError represents a transport- or protocol-level failure that isn't
// a graceful end-of-document condition (see ScanComplete).
type ScsiError struct {
	Opcode  byte
	Message string
}

func (e *ScsiError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("scsiusb: command %#02x failed", e.Opcode)
	}
	return fmt.Sprintf("scsiusb: command %#02x: %s", e.Opcode, e.Message)
}

// CheckConditionError is returned when a command's status byte indicates
// a SCSI CHECK CONDITION (0x02). It carries the raw sense buffer fetched
// via REQUEST SENSE; scsiusb itself never interprets sense codes — that
// meaning is device-specific and is the caller's (driver package's)
// responsibility to classify.
type CheckConditionError struct {
	Sense []byte
}

func (e *CheckConditionError) Error() string {
	return fmt.Sprintf("scsiusb: check condition, sense=% x", e.Sense)
}

// ScanComplete is a sentinel error a device-specific sense classifier can
// return (wrapped from a CheckConditionError) to signal a normal
// end-of-document condition rather than a real error. Callers should
// check for it with errors.Is/errors.As and treat it as success, not
// failure.
type ScanComplete struct {
	Reason string
}

func (e *ScanComplete) Error() string {
	return fmt.Sprintf("scan complete: %s", e.Reason)
}
