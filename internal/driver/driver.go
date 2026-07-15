// Package driver defines the minimal, deliberately small interface a
// scanner implementation must satisfy to be usable by this application.
// It exists so a second scanner model could be added later (reusing
// internal/scsiusb for the generic USB-bulk/SCSI-CDB transport) without
// touching any of the job/storage/API/frontend code, which only ever
// talks to a Driver/Session, never to a concrete scanner package
// directly.
package driver

import (
	"context"
	"image"
)

// Info describes a driver for display/diagnostic purposes.
type Info struct {
	Name   string // registry key, e.g. "doxie-dx400"
	Vendor string
	Model  string
	VID    uint16
	PID    uint16
}

// ScanOptions controls how a single page is scanned.
type ScanOptions struct {
	// Duplex requests both sides of the page. Not all drivers support
	// this; drivers that don't should simply ignore it and always scan
	// simplex.
	Duplex bool
}

// Page is the result of scanning one physical sheet.
type Page struct {
	Front image.Image
	// Back is nil for a simplex scan, or for a duplex scan whose back
	// side was detected as blank and dropped.
	Back image.Image
	// BackBlank records whether a duplex scan's back side was detected
	// as blank (and therefore Back is nil), so callers can distinguish
	// "no back side requested" from "back side requested but blank."
	BackBlank bool
}

// Session represents one open connection to a scanner, spanning
// potentially many pages (e.g. a whole ADF batch).
type Session interface {
	// HasNextPage reports whether the feeder currently has a sheet ready
	// to scan. Callers should stop looping once this returns false.
	HasNextPage(ctx context.Context) (bool, error)
	// ScanPage scans the next sheet. Callers must have confirmed
	// HasNextPage first.
	ScanPage(ctx context.Context, opts ScanOptions) (Page, error)
	// Close releases the underlying device.
	Close() error
}

// Driver is a scanner model implementation.
type Driver interface {
	Info() Info
	// Detect reports whether the scanner is currently present and
	// reachable, without starting a scan session. Used for the
	// connected/disconnected status indicator.
	Detect(ctx context.Context) error
	// Open opens a session for scanning. Callers should call Detect
	// first if they want a clean "not connected" error distinct from an
	// Open-time failure, though Open may return the same error types.
	Open(ctx context.Context) (Session, error)
}
