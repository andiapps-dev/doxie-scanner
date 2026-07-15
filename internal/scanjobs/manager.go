package scanjobs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/png"
	"sync"
	"time"

	"github.com/andiapps-dev/doxie-scanner/internal/driver"
	"github.com/andiapps-dev/doxie-scanner/internal/storage"
)

// ErrAlreadyRunning is returned by StartScan when a scan is already in
// progress — there's exactly one physical scanner, so there's no queue,
// just a single "busy" rejection.
var ErrAlreadyRunning = errors.New("scanjobs: a scan is already in progress")

// ScanDPI is the fixed resolution this application scans at, recorded in
// job metadata for display purposes.
const ScanDPI = 300

// Manager orchestrates scan jobs against a driver.Driver, persisting
// results via a storage.Store.
type Manager struct {
	drv   driver.Driver
	store *storage.Store
	now   func() time.Time

	mu      sync.Mutex
	running *JobState // most recently started job's live state (nil until the first scan)
}

// NewManager returns a Manager for the given driver and store.
func NewManager(drv driver.Driver, store *storage.Store) *Manager {
	return &Manager{drv: drv, store: store, now: time.Now}
}

// CurrentJob returns a snapshot of the most recently started job's live
// state, or nil if no scan has ever been started. Callers should treat
// this as authoritative only while Status == storage.StatusRunning;
// once finished, storage.Store holds the authoritative persisted record
// (this in-memory copy is just for live progress during a scan, and
// doesn't survive a process restart).
func (m *Manager) CurrentJob() *JobState {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.running == nil {
		return nil
	}
	cp := *m.running
	return &cp
}

// StartScan begins a new scan job in the background and returns its job
// ID immediately; the caller polls CurrentJob (or re-reads the job from
// storage once it's no longer the current job) for progress. Returns
// ErrAlreadyRunning if a scan is already in progress.
func (m *Manager) StartScan(duplex bool) (string, error) {
	m.mu.Lock()
	if m.running != nil && m.running.Status == storage.StatusRunning {
		m.mu.Unlock()
		return "", ErrAlreadyRunning
	}
	now := m.now()
	id := storage.NewJobID(now)
	m.running = &JobState{ID: id, Status: storage.StatusRunning}
	m.mu.Unlock()

	meta := storage.JobMeta{
		ID:        id,
		Name:      storage.NewJobName(now),
		Driver:    m.drv.Info().Name,
		CreatedAt: now,
		Status:    storage.StatusRunning,
		Duplex:    duplex,
		DPI:       ScanDPI,
	}
	if err := m.store.CreateJob(meta); err != nil {
		m.mu.Lock()
		m.running.Status = storage.StatusFailed
		m.running.Err = err
		m.mu.Unlock()
		return "", fmt.Errorf("scanjobs: create job: %w", err)
	}

	// The scan must outlive whatever request triggered it, so it runs
	// against its own background context, not the caller's.
	go m.run(context.Background(), id, duplex, meta)

	return id, nil
}

// run drives the actual scan loop. It's a separate method (rather than
// inlined into the goroutine literal in StartScan) specifically so tests
// can call it synchronously without needing to poll for a background
// goroutine to finish.
func (m *Manager) run(ctx context.Context, id string, duplex bool, meta storage.JobMeta) {
	sess, err := m.drv.Open(ctx)
	if err != nil {
		m.finish(id, &meta, err)
		return
	}
	defer sess.Close()

	nextIndex := 0
	for {
		hasNext, err := sess.HasNextPage(ctx)
		if err != nil {
			m.finish(id, &meta, err)
			return
		}
		if !hasNext {
			break
		}

		sheet, err := sess.ScanPage(ctx, driver.ScanOptions{Duplex: duplex})
		if err != nil {
			m.finish(id, &meta, err)
			return
		}

		// Each side of a physical sheet becomes its own independent page,
		// with its own sequential number — a duplex sheet yields two
		// pages (e.g. 1 and 2), not one page with an attached back image.
		// A blank detected back (sheet.Back == nil) simply doesn't
		// produce a second page.
		nextIndex++
		frontMeta, err := m.saveImage(id, nextIndex, sheet.Front)
		if err != nil {
			m.finish(id, &meta, err)
			return
		}
		meta.Pages = append(meta.Pages, frontMeta)

		if sheet.Back != nil {
			nextIndex++
			backMeta, err := m.saveImage(id, nextIndex, sheet.Back)
			if err != nil {
				m.finish(id, &meta, err)
				return
			}
			meta.Pages = append(meta.Pages, backMeta)
		}

		meta.PageCount = len(meta.Pages)
		_ = m.store.SaveMeta(meta) // best-effort progress checkpoint; final save happens at completion regardless

		m.mu.Lock()
		if m.running != nil && m.running.ID == id {
			m.running.PagesScanned = len(meta.Pages)
		}
		m.mu.Unlock()
	}

	m.finish(id, &meta, nil)
}

// finish records a job's terminal state, both in the persisted metadata
// and the in-memory live state.
func (m *Manager) finish(id string, meta *storage.JobMeta, runErr error) {
	now := m.now()
	meta.CompletedAt = &now
	if runErr != nil {
		meta.Status = storage.StatusFailed
		meta.Error = runErr.Error()
	} else {
		meta.Status = storage.StatusCompleted
	}
	_ = m.store.SaveMeta(*meta)

	m.mu.Lock()
	if m.running != nil && m.running.ID == id {
		m.running.Status = meta.Status
		m.running.Err = runErr
	}
	m.mu.Unlock()
}

// saveImage encodes one scanned image (either side of a sheet) as PNG and
// persists it under the given page index, returning the metadata record
// to append to the job's page list.
func (m *Manager) saveImage(jobID string, index int, img image.Image) (storage.PageMeta, error) {
	data, err := encodePNG(img)
	if err != nil {
		return storage.PageMeta{}, fmt.Errorf("scanjobs: encode page %d: %w", index, err)
	}
	file := storage.PageFilename(index)
	if err := m.store.SavePageFile(jobID, file, data); err != nil {
		return storage.PageMeta{}, err
	}
	return storage.PageMeta{
		Index:    index,
		File:     file,
		WidthPx:  img.Bounds().Dx(),
		HeightPx: img.Bounds().Dy(),
	}, nil
}

// pngEncode is a package-level indirection purely so tests can exercise
// the encode-failure path (extremely hard to trigger with any real
// image.Image, since driver implementations never hand back one that
// png.Encode itself rejects) without needing a contrived image value.
var pngEncode = png.Encode

func encodePNG(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	if err := pngEncode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
