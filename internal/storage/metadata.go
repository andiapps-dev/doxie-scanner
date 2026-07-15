// Package storage persists scan jobs to the filesystem under a single
// required root directory (DOXIE_DATA_DIR in production — see main.go).
// There is deliberately no database: one directory per job containing a
// meta.json sidecar and a pages/ subdirectory of PNG files is enough for
// a tool with this usage pattern, and it keeps the whole thing
// inspectable with plain filesystem tools.
package storage

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

// JobStatus is the lifecycle state of a scan job.
type JobStatus string

const (
	StatusRunning   JobStatus = "running"
	StatusCompleted JobStatus = "completed"
	StatusFailed    JobStatus = "failed"
)

// PageMeta describes one scanned page within a job. Each side of a
// duplex sheet is its own independent PageMeta with its own sequential
// Index — a duplex sheet produces two entries (e.g. indexes 1 and 2),
// not one entry with a paired back image. There is nothing here that
// distinguishes "front" from "back": once scanned, every page stands on
// its own (rotate/crop/delete/export/reorder all act on one page at a
// time, uniformly).
type PageMeta struct {
	Index    int    `json:"index"`
	File     string `json:"file"`
	WidthPx  int    `json:"widthPx"`
	HeightPx int    `json:"heightPx"`
}

// JobMeta is the full persisted record for one scan job, stored as
// meta.json inside that job's directory.
type JobMeta struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Driver      string     `json:"driver"`
	CreatedAt   time.Time  `json:"createdAt"`
	CompletedAt *time.Time `json:"completedAt,omitempty"`
	Status      JobStatus  `json:"status"`
	Duplex      bool       `json:"duplex"`
	DPI         int        `json:"dpi"`
	PageCount   int        `json:"pageCount"`
	Pages       []PageMeta `json:"pages"`
	Error       string     `json:"error,omitempty"`
}

// NewJobID returns a human-sortable, collision-resistant job identifier:
// a UTC timestamp (to the second) plus 4 random hex characters, so two
// jobs started within the same second still get distinct IDs.
func NewJobID(now time.Time) string {
	return fmt.Sprintf("%s-%s", now.UTC().Format("20060102-150405"), randHex(4))
}

// NewJobName returns the auto-generated display name assigned to a job
// at creation time (e.g. "Scan 2026-07-14 15:30"). Callers can rename a
// job afterward via the API; this is only ever the initial default.
func NewJobName(now time.Time) string {
	return "Scan " + now.Format("2006-01-02 15:04")
}

// cryptoRandRead is a package-level indirection purely so tests can
// exercise the (effectively unreachable in practice) failure path
// without needing to actually break the host's entropy source.
var cryptoRandRead = rand.Read

func randHex(n int) string {
	buf := make([]byte, (n+1)/2)
	if _, err := cryptoRandRead(buf); err != nil {
		// crypto/rand failing is effectively unrecoverable and would
		// indicate a broken host; panicking here matches how the
		// standard library itself treats a failing rand.Read in
		// contexts where there is no sane fallback.
		panic("storage: crypto/rand unavailable: " + err.Error())
	}
	return hex.EncodeToString(buf)[:n]
}

// PageFilename returns the on-disk filename for a page.
func PageFilename(index int) string {
	return fmt.Sprintf("page-%03d.png", index)
}
