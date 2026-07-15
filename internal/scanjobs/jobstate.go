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
}
