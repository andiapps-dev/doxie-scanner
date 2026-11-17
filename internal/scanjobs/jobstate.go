// Package scanjobs orchestrates scan jobs: it drives a driver.Session's
// HasNextPage/ScanPage loop, persists each page via storage.Store as it
// arrives, and tracks live in-memory progress so the HTTP API can report
// a running job's status without waiting for it to finish. There is
// deliberately no job queue — this hardware is one physical device, so
// only one job can be in progress at a time.
package scanjobs

import "github.com/andiapps-dev/doxie-scanner/internal/storage"

// JobState is the in-memory live view of the most recent (or currently
// running) scan job. It exists alongside the persisted storage.JobMeta
// specifically so a client polling for progress mid-scan can see
// PagesScanned update in real time, without the API needing to re-read
// and re-parse meta.json on every poll.
type JobState struct {
	ID           string
	Status       storage.JobStatus
	PagesScanned int
	Err          error

	// Stage is what the running job is doing right now, one of the Stage
	// constants, so the UI can say why a scan that has stopped feeding is
	// still busy. StageDone and StageTotal give its progress where it has
	// one: the sheet number while scanning, pages turned so far of the
	// total while turning pages upright. They are zero otherwise.
	Stage      string
	StageDone  int
	StageTotal int
}

// What a running job can be doing, in the order it normally does them.
const (
	StageChecking  = "checking"  // asking the scanner whether there is more paper
	StageScanning  = "scanning"  // a sheet is feeding; StageDone is its number
	StageSaving    = "saving"    // turning the sheet's scan into stored pages
	StageReleasing = "releasing" // letting the scanner go
	StageCropping  = "cropping"  // trimming pages to their sheet; StageDone of StageTotal
	StageRotating  = "rotating"  // turning pages upright; StageDone of StageTotal
)
