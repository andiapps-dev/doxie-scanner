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

	"github.com/andiapps-dev/doxie-scanner/internal/autocrop"
	"github.com/andiapps-dev/doxie-scanner/internal/driver"
	"github.com/andiapps-dev/doxie-scanner/internal/orient"
	"github.com/andiapps-dev/doxie-scanner/internal/pageedit"
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
	drv      driver.Driver
	store    *storage.Store
	now      func() time.Time
	orienter orient.Func // nil: auto-rotate unavailable

	mu      sync.Mutex
	running *JobState // most recently started job's live state (nil until the first scan)
}

// NewManager returns a Manager for the given driver and store.
func NewManager(drv driver.Driver, store *storage.Store) *Manager {
	return &Manager{drv: drv, store: store, now: time.Now}
}

// WithOrienter lets scans be started with auto-rotate: once the sheets
// are scanned and the scanner released, each page is turned upright using
// detect. It returns m for chaining.
func (m *Manager) WithOrienter(detect orient.Func) *Manager {
	m.orienter = detect
	return m
}

// Orienter is the detector given to WithOrienter, or nil.
func (m *Manager) Orienter() orient.Func { return m.orienter }

// CanAutoRotate reports whether WithOrienter has been given a detector.
func (m *Manager) CanAutoRotate() bool { return m.orienter != nil }

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
	return m.StartScanWith(Options{Duplex: duplex})
}

// Options are the choices for one scan.
type Options struct {
	Duplex bool
	// AutoRotate turns each page upright once the scan has finished. It is
	// ignored when the manager has no detector.
	AutoRotate bool
	// AutoCrop trims each page to its sheet (photos, cards, receipts) and
	// straightens it, once the scan has finished.
	AutoCrop bool
}

// StartScanWith is StartScan with the post-processing choices.
func (m *Manager) StartScanWith(opts Options) (string, error) {
	duplex := opts.Duplex
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

		AutoRotate: opts.AutoRotate && m.orienter != nil,
		AutoCrop:   opts.AutoCrop,
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
	// The scanner is released before any post-processing, so slow work
	// like auto-rotate never holds it.
	closeSession := sync.OnceFunc(func() { _ = sess.Close() })
	defer closeSession()

	nextIndex, sheets := 0, 0
	for {
		m.setStage(id, StageChecking, 0, 0)
		hasNext, err := sess.HasNextPage(ctx)
		if err != nil {
			m.finish(id, &meta, err)
			return
		}
		if !hasNext {
			break
		}

		sheets++
		m.setStage(id, StageScanning, sheets, 0)
		sheet, err := sess.ScanPage(ctx, driver.ScanOptions{Duplex: duplex})
		if err != nil {
			m.finish(id, &meta, err)
			return
		}
		m.setStage(id, StageSaving, sheets, 0)

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

	m.setStage(id, StageReleasing, 0, 0)
	closeSession()
	// Crop before rotating: the page is smaller for the text reader, and
	// the two don't interfere. Per-page problems in either are not scan
	// failures: a page that can't be processed is left as scanned.
	if meta.AutoCrop {
		_, _ = AutoCropProgress(m.store, &meta, func(done, total int) {
			m.setStage(id, StageCropping, done, total)
		})
	}
	if meta.AutoRotate {
		_, _ = AutoRotateProgress(m.store, m.orienter, &meta, func(done, total int) {
			m.setStage(id, StageRotating, done, total)
		})
	}
	m.finish(id, &meta, nil)
}

// AutoCrop trims each of meta's pages to its sheet, as AutoCropProgress.
func AutoCrop(store *storage.Store, meta *storage.JobMeta) (int, error) {
	return AutoCropProgress(store, meta, nil)
}

// AutoCropProgress trims each of meta's pages to its sheet and straightens
// it (see internal/autocrop for when it acts), rewriting the stored image.
// A page it changes first has its as-scanned image kept beside it, so the
// change can be undone; a page already holding one keeps that, which stays
// the true original. It returns how many pages it changed and saves
// nothing: the caller saves meta. progress, when not nil, is called before
// each page with its number and the total, counting from 1.
func AutoCropProgress(store *storage.Store, meta *storage.JobMeta, progress func(done, total int)) (int, error) {
	changed := 0
	for i := range meta.Pages {
		if progress != nil {
			progress(i+1, len(meta.Pages))
		}
		page := &meta.Pages[i]
		data, err := store.LoadPageFile(meta.ID, page.File)
		if err != nil {
			continue
		}
		img, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			continue
		}
		cropped, res, ok := autocrop.Process(img)
		if !ok {
			continue
		}
		out, err := encodePNG(cropped)
		if err != nil {
			continue
		}
		original := page.Original
		if original == "" {
			original = storage.OriginalFilename(page.Index)
			if err := store.SavePageFile(meta.ID, original, data); err != nil {
				continue // never change a page whose original could not be kept
			}
		}
		if err := store.SavePageFile(meta.ID, page.File, out); err != nil {
			continue
		}
		page.Original = original
		page.WidthPx, page.HeightPx = cropped.Bounds().Dx(), cropped.Bounds().Dy()
		page.AutoCrop = &storage.AutoCropInfo{
			X: res.Crop.X, Y: res.Crop.Y, W: res.Crop.W, H: res.Crop.H,
			Straighten: res.Straighten, FromWidth: res.FromWidth, FromHeight: res.FromHeight,
		}
		changed++
	}
	return changed, nil
}

// setStage records what job id is doing, for CurrentJob.
func (m *Manager) setStage(id, stage string, done, total int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.running != nil && m.running.ID == id {
		m.running.Stage, m.running.StageDone, m.running.StageTotal = stage, done, total
	}
}

// AutoRotate turns each of meta's pages upright using detect, rewriting
// the stored image and recording the turn in the page's metadata. A page
// the detector can't read (a blank back, a photo) is left alone. It
// returns how many pages it turned and saves nothing: the caller saves
// meta. Only a missing tool stops it early.
func AutoRotate(store *storage.Store, detect orient.Func, meta *storage.JobMeta) (int, error) {
	return AutoRotateProgress(store, detect, meta, nil)
}

// AutoRotateProgress is AutoRotate that calls progress (when not nil)
// before each page with the number of the page it is about to read and
// the total, counting from 1.
func AutoRotateProgress(store *storage.Store, detect orient.Func, meta *storage.JobMeta, progress func(done, total int)) (int, error) {
	turned := 0
	for i := range meta.Pages {
		if progress != nil {
			progress(i+1, len(meta.Pages))
		}
		page := &meta.Pages[i]
		data, err := store.LoadPageFile(meta.ID, page.File)
		if err != nil {
			continue
		}
		img, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			continue
		}
		res, err := detect(img)
		if errors.Is(err, orient.ErrToolNotAvailable) {
			return turned, err
		}
		if err != nil || res.Rotate == 0 {
			continue
		}
		upright, err := pageedit.Rotate(img, res.Rotate)
		if err != nil {
			continue // not a quarter turn: nothing sensible to do
		}
		out, err := encodePNG(upright)
		if err != nil {
			continue
		}
		if err := store.SavePageFile(meta.ID, page.File, out); err != nil {
			continue
		}
		page.WidthPx, page.HeightPx = upright.Bounds().Dx(), upright.Bounds().Dy()
		page.AutoRotated = res.Rotate
		turned++
	}
	return turned, nil
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
		m.running.Stage, m.running.StageDone, m.running.StageTotal = "", 0, 0
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
