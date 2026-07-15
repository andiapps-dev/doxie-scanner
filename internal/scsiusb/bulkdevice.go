// Package scsiusb implements a generic, device-agnostic transport for
// talking to USB scanners that accept standard SCSI Command Descriptor
// Blocks (CDBs) over plain USB bulk transfers instead of a real SCSI bus.
//
// It knows nothing about any specific scanner model's opcodes, window
// parameters, or sense-code meanings — that lives in a device-specific
// package (e.g. internal/doxiedx400). This package only knows how to pad
// and send a CDB, optionally write/read a payload, read the one-byte
// status every command is followed by, retry on transport hiccups, and
// surface a check-condition's sense data for the caller to interpret.
package scsiusb

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
