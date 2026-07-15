package scanjobs

import (
	"context"
	"image"
	"image/color"
	"io"
	"os"
	"testing"
	"time"

	"github.com/andiapps-dev/doxie-scanner/internal/driver"
	"github.com/andiapps-dev/doxie-scanner/internal/storage"
)

func testImage(w, h int, c color.NRGBA) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	return img
}

func waitForJobDone(t *testing.T, m *Manager) *JobState {
	t.Helper()
	// Generous margin: these fakes finish almost instantly in practice, but
	// a shared/throttled CI runner can occasionally starve the background
	// goroutine of scheduling time for longer than a tight deadline allows,
	// producing a flaky failure on otherwise-identical code (observed once
	// in CI, unreproducible locally, and immediately green on retry).
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		state := m.CurrentJob()
		if state != nil && state.Status != storage.StatusRunning {
			return state
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("timed out waiting for job to finish")
	return nil
}

func newTestManager(t *testing.T, drv driver.Driver) (*Manager, *storage.Store) {
	t.Helper()
	store := storage.New(t.TempDir())
	m := NewManager(drv, store)
	return m, store
}

func TestManager_CurrentJob_NilBeforeAnyScan(t *testing.T) {
	m, _ := newTestManager(t, &fakeDriver{session: &fakeSession{}})
	if m.CurrentJob() != nil {
		t.Error("expected nil before any scan has started")
	}
}

func TestManager_StartScan_ZeroPages(t *testing.T) {
	drv := &fakeDriver{info: driver.Info{Name: "doxie-dx400"}, session: &fakeSession{}}
	m, store := newTestManager(t, drv)

	id, err := m.StartScan(false)
	if err != nil {
		t.Fatalf("StartScan: %v", err)
	}
	state := waitForJobDone(t, m)
	if state.Status != storage.StatusCompleted {
		t.Errorf("status: got %v", state.Status)
	}
	if state.PagesScanned != 0 {
		t.Errorf("pagesScanned: got %d", state.PagesScanned)
	}

	meta, err := store.LoadMeta(id)
	if err != nil {
		t.Fatalf("LoadMeta: %v", err)
	}
	if meta.PageCount != 0 || meta.Status != storage.StatusCompleted {
		t.Errorf("meta: got %+v", meta)
	}
	if !drv.session.closed {
		t.Error("expected the session to be closed")
	}
}

func TestManager_StartScan_SimplexPages(t *testing.T) {
	page1 := driver.Page{Front: testImage(4, 4, color.NRGBA{R: 255, A: 255})}
	page2 := driver.Page{Front: testImage(4, 4, color.NRGBA{G: 255, A: 255})}
	drv := &fakeDriver{session: &fakeSession{pages: []driver.Page{page1, page2}}}
	m, store := newTestManager(t, drv)

	id, err := m.StartScan(false)
	if err != nil {
		t.Fatalf("StartScan: %v", err)
	}
	state := waitForJobDone(t, m)
	if state.Status != storage.StatusCompleted {
		t.Fatalf("status: got %v, err=%v", state.Status, state.Err)
	}
	if state.PagesScanned != 2 {
		t.Errorf("pagesScanned: got %d", state.PagesScanned)
	}

	meta, err := store.LoadMeta(id)
	if err != nil {
		t.Fatalf("LoadMeta: %v", err)
	}
	if meta.PageCount != 2 {
		t.Fatalf("pageCount: got %d", meta.PageCount)
	}
	for _, p := range meta.Pages {
		data, err := store.LoadPageFile(id, p.File)
		if err != nil {
			t.Fatalf("LoadPageFile(%s): %v", p.File, err)
		}
		if len(data) == 0 {
			t.Errorf("page %d: empty PNG data", p.Index)
		}
	}
}

func TestManager_StartScan_DuplexWithNonBlankBack(t *testing.T) {
	page := driver.Page{
		Front: testImage(4, 4, color.NRGBA{R: 255, A: 255}),
		Back:  testImage(4, 4, color.NRGBA{B: 255, A: 255}),
	}
	drv := &fakeDriver{session: &fakeSession{pages: []driver.Page{page}}}
	m, store := newTestManager(t, drv)

	id, err := m.StartScan(true)
	if err != nil {
		t.Fatalf("StartScan: %v", err)
	}
	waitForJobDone(t, m)

	meta, err := store.LoadMeta(id)
	if err != nil {
		t.Fatal(err)
	}
	// Front and back each become their own independent page: 2 pages
	// from 1 physical sheet, not 1 page with an attached back image.
	if len(meta.Pages) != 2 {
		t.Fatalf("expected 2 independent pages from one duplex sheet, got %+v", meta.Pages)
	}
	if meta.Pages[0].Index != 1 || meta.Pages[1].Index != 2 {
		t.Fatalf("expected sequential indexes 1,2, got %d,%d", meta.Pages[0].Index, meta.Pages[1].Index)
	}
	if _, err := store.LoadPageFile(id, meta.Pages[0].File); err != nil {
		t.Errorf("front file not saved: %v", err)
	}
	if _, err := store.LoadPageFile(id, meta.Pages[1].File); err != nil {
		t.Errorf("back file not saved: %v", err)
	}
}

func TestManager_StartScan_DuplexBlankBackDropped(t *testing.T) {
	page := driver.Page{
		Front:     testImage(4, 4, color.NRGBA{R: 255, A: 255}),
		Back:      nil,
		BackBlank: true,
	}
	drv := &fakeDriver{session: &fakeSession{pages: []driver.Page{page}}}
	m, store := newTestManager(t, drv)

	id, err := m.StartScan(true)
	if err != nil {
		t.Fatalf("StartScan: %v", err)
	}
	waitForJobDone(t, m)

	meta, err := store.LoadMeta(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(meta.Pages) != 1 {
		t.Fatalf("expected only the front page (blank back dropped), got %+v", meta.Pages)
	}
}

// TestManager_StartScan_MixedDuplexAndSimplexNumbering matches the exact
// scenario a user described: one duplex sheet followed by one simplex
// sheet should number 1, 2 (the duplex sheet's front/back), then 3 (the
// simplex sheet) — page numbers track saved images, not physical sheets.
func TestManager_StartScan_MixedDuplexAndSimplexNumbering(t *testing.T) {
	duplexSheet := driver.Page{
		Front: testImage(4, 4, color.NRGBA{R: 255, A: 255}),
		Back:  testImage(4, 4, color.NRGBA{G: 255, A: 255}),
	}
	simplexSheet := driver.Page{Front: testImage(4, 4, color.NRGBA{B: 255, A: 255})}
	drv := &fakeDriver{session: &fakeSession{pages: []driver.Page{duplexSheet, simplexSheet}}}
	m, store := newTestManager(t, drv)

	id, err := m.StartScan(true)
	if err != nil {
		t.Fatalf("StartScan: %v", err)
	}
	waitForJobDone(t, m)

	meta, err := store.LoadMeta(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(meta.Pages) != 3 {
		t.Fatalf("expected 3 pages total, got %+v", meta.Pages)
	}
	var got []int
	for _, p := range meta.Pages {
		got = append(got, p.Index)
	}
	want := []int{1, 2, 3}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("page indexes = %v, want %v", got, want)
		}
	}
}

func TestManager_StartScan_AlreadyRunning(t *testing.T) {
	// A session with a page that never completes HasNextPage quickly is
	// hard to hold "running" deterministically without a hook, so
	// instead we mark m.running manually to simulate a scan already in
	// progress, avoiding any timing dependency.
	m, _ := newTestManager(t, &fakeDriver{session: &fakeSession{}})
	m.running = &JobState{ID: "existing-job", Status: storage.StatusRunning}

	if _, err := m.StartScan(false); err != ErrAlreadyRunning {
		t.Fatalf("got %v, want ErrAlreadyRunning", err)
	}
}

func TestManager_StartScan_OpenError(t *testing.T) {
	drv := &fakeDriver{openErr: errTest("no device")}
	m, store := newTestManager(t, drv)

	id, err := m.StartScan(false)
	if err != nil {
		t.Fatalf("StartScan should succeed synchronously even though the background run fails: %v", err)
	}
	state := waitForJobDone(t, m)
	if state.Status != storage.StatusFailed {
		t.Errorf("status: got %v", state.Status)
	}
	if state.Err == nil {
		t.Error("expected a recorded error")
	}

	meta, err := store.LoadMeta(id)
	if err != nil {
		t.Fatal(err)
	}
	if meta.Status != storage.StatusFailed || meta.Error == "" {
		t.Errorf("meta: got %+v", meta)
	}
}

func TestManager_StartScan_HasNextPageError(t *testing.T) {
	drv := &fakeDriver{session: &fakeSession{hasNextErr: errTest("media check failed")}}
	m, _ := newTestManager(t, drv)

	if _, err := m.StartScan(false); err != nil {
		t.Fatalf("StartScan: %v", err)
	}
	state := waitForJobDone(t, m)
	if state.Status != storage.StatusFailed {
		t.Errorf("status: got %v", state.Status)
	}
}

func TestManager_StartScan_ScanPageErrorMidLoop(t *testing.T) {
	page1 := driver.Page{Front: testImage(4, 4, color.NRGBA{R: 255, A: 255})}
	page2 := driver.Page{Front: testImage(4, 4, color.NRGBA{G: 255, A: 255})}
	drv := &fakeDriver{session: &fakeSession{
		pages:     []driver.Page{page1, page2},
		scanErr:   errTest("scsi error"),
		scanErrAt: 2,
	}}
	m, store := newTestManager(t, drv)

	id, err := m.StartScan(false)
	if err != nil {
		t.Fatalf("StartScan: %v", err)
	}
	state := waitForJobDone(t, m)
	if state.Status != storage.StatusFailed {
		t.Fatalf("status: got %v", state.Status)
	}
	if state.PagesScanned != 1 {
		t.Errorf("expected page 1 to have been saved before the failure, got pagesScanned=%d", state.PagesScanned)
	}

	meta, err := store.LoadMeta(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(meta.Pages) != 1 {
		t.Errorf("expected page 1's metadata to be persisted despite the later failure, got %d pages", len(meta.Pages))
	}
}

func TestManager_StartScan_CreateJobError(t *testing.T) {
	drv := &fakeDriver{session: &fakeSession{}}
	// Use a store whose root can never be created (a file in the way).
	badRoot := t.TempDir() + "/blocker"
	if err := writeFile(badRoot, "x"); err != nil {
		t.Fatal(err)
	}
	store := storage.New(badRoot + "/data")
	m := NewManager(drv, store)

	if _, err := m.StartScan(false); err == nil {
		t.Fatal("expected an error")
	}
}

func TestManager_StartScan_SavePageError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: permission-based test is unreliable")
	}
	page := driver.Page{Front: testImage(4, 4, color.NRGBA{R: 255, A: 255})}
	drv := &fakeDriver{session: &fakeSession{pages: []driver.Page{page}}}
	m, store := newTestManager(t, drv)

	// Drive run() directly (synchronously) instead of via StartScan's
	// background goroutine, so the pages directory can be made
	// unwritable deterministically before the scan ever touches it —
	// no timing race against a concurrent goroutine.
	now := time.Now()
	id := storage.NewJobID(now)
	meta := storage.JobMeta{
		ID: id, Name: storage.NewJobName(now), Driver: drv.Info().Name,
		CreatedAt: now, Status: storage.StatusRunning,
	}
	if err := store.CreateJob(meta); err != nil {
		t.Fatal(err)
	}
	m.running = &JobState{ID: id, Status: storage.StatusRunning}

	pagesDir := store.Root() + "/jobs/" + id + "/pages"
	if err := os.Chmod(pagesDir, 0o555); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(pagesDir, 0o755)

	m.run(context.Background(), id, false, meta)

	state := m.CurrentJob()
	if state.Status != storage.StatusFailed {
		t.Fatalf("status: got %v (err=%v)", state.Status, state.Err)
	}
}

func TestManager_StartScan_BackSaveErrorMidLoop(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: permission-based test is unreliable")
	}
	page := driver.Page{
		Front: testImage(4, 4, color.NRGBA{R: 255, A: 255}),
		Back:  testImage(4, 4, color.NRGBA{B: 255, A: 255}),
	}
	drv := &fakeDriver{session: &fakeSession{pages: []driver.Page{page}}}
	m, store := newTestManager(t, drv)

	now := time.Now()
	id := storage.NewJobID(now)
	meta := storage.JobMeta{
		ID: id, Name: storage.NewJobName(now), Driver: drv.Info().Name,
		CreatedAt: now, Status: storage.StatusRunning,
	}
	if err := store.CreateJob(meta); err != nil {
		t.Fatal(err)
	}
	m.running = &JobState{ID: id, Status: storage.StatusRunning}

	// The front page (index 1) will be a fresh file the pages/ directory
	// happily creates; pre-creating the back page's (index 2) target
	// file read-only makes only its save fail, after the front already
	// succeeded — exercising the mid-sheet failure path specifically.
	backPath := store.Root() + "/jobs/" + id + "/pages/" + storage.PageFilename(2)
	if err := os.WriteFile(backPath, []byte("placeholder"), 0o444); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(backPath, 0o644)

	m.run(context.Background(), id, true, meta)

	state := m.CurrentJob()
	if state.Status != storage.StatusFailed {
		t.Fatalf("status: got %v (err=%v)", state.Status, state.Err)
	}

	persisted, err := store.LoadMeta(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(persisted.Pages) != 1 || persisted.Pages[0].Index != 1 {
		t.Errorf("expected only the front page to have been persisted, got %+v", persisted.Pages)
	}
}

func TestManager_SaveImage_EncodeError(t *testing.T) {
	original := pngEncode
	pngEncode = func(w io.Writer, img image.Image) error { return errTest("simulated encode failure") }
	defer func() { pngEncode = original }()

	store := storage.New(t.TempDir())
	m := NewManager(&fakeDriver{}, store)
	if err := store.CreateJob(storage.JobMeta{ID: "job1"}); err != nil {
		t.Fatal(err)
	}

	img := testImage(2, 2, color.NRGBA{R: 255, A: 255})
	if _, err := m.saveImage("job1", 1, img); err == nil {
		t.Fatal("expected an error")
	}
}

func TestManager_SaveImage_SaveFileError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: permission-based test is unreliable")
	}
	store := storage.New(t.TempDir())
	m := NewManager(&fakeDriver{}, store)
	if err := store.CreateJob(storage.JobMeta{ID: "job1"}); err != nil {
		t.Fatal(err)
	}
	// Pre-create the target file read-only so writing it fails.
	path := store.Root() + "/jobs/job1/pages/" + storage.PageFilename(1)
	if err := os.WriteFile(path, []byte("placeholder"), 0o444); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(path, 0o644)

	img := testImage(2, 2, color.NRGBA{R: 255, A: 255})
	if _, err := m.saveImage("job1", 1, img); err == nil {
		t.Fatal("expected an error")
	}
}

type errTest string

func (e errTest) Error() string { return string(e) }

func writeFile(path, contents string) error {
	return os.WriteFile(path, []byte(contents), 0o644)
}
