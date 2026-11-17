// Package scsiusb is the USB transport for Avision-family scanners that
// take SCSI-style Command Descriptor Blocks over USB instead of a SCSI
// bus, using the 12-byte framing Doxie's own DX400 driver speaks.
//
// It knows nothing about any specific scanner model's opcodes, window
// parameters, or sense-code meanings — that lives in a device-specific
// package (e.g. internal/doxiedx400). This package only sends a CDB,
// optionally writes/reads a payload, reads the status every command is
// followed by, retries transport hiccups, and surfaces a check
// condition's sense data for the caller to interpret.
package scsiusb

import "context"

// BulkDevice is the minimal seam between this package's protocol-level
// logic and a real USB device. The only implementation that talks to real
// hardware is the gousb-backed one in usbdevice.go; everywhere else in
// this codebase, tests exercise the logic in this package against a fake
// BulkDevice instead, so none of it depends on physical hardware to test.
type BulkDevice interface {
	// Write sends p over the bulk-OUT endpoint and returns the number of
	// bytes written.
	Write(p []byte) (int, error)
	// Read reads up to len(p) bytes from the bulk-IN endpoint into p and
	// returns the number of bytes read. A short read (n < len(p)) with a
	// nil error is valid and expected — callers loop until they have as
	// many bytes as they asked for, or treat a zero-length read as EOF
	// depending on context.
	Read(p []byte) (int, error)
	// Close releases the underlying device/interface/context.
	Close() error
}

// InterruptReader is implemented by devices with an interrupt IN pipe,
// which Avision scanners use to report button presses.
type InterruptReader interface {
	ReadInterrupt(ctx context.Context, p []byte) (int, error)
}

// CommandSender is implemented by devices that can deliver a CDB as a
// USB vendor control request, which is how Doxie's own driver sends
// 12-byte commands. Command12 falls back to bulk OUT otherwise.
type CommandSender interface {
	SendCommand(cdb []byte) error
}
