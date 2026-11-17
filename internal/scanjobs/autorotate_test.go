package scanjobs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"strings"
	"testing"

	"github.com/andiapps-dev/doxie-scanner/internal/driver"
	"github.com/andiapps-dev/doxie-scanner/internal/orient"
	"github.com/andiapps-dev/doxie-scanner/internal/storage"
)

// detectByWidth is a fake detector: wide pages are sideways (turn 90),
// pages exactly 7 wide are unreadable, everything else is upright.
func detectByWidth(img image.Image) (orient.Result, error) {
	switch w := img.Bounds().Dx(); {
	case w == 7:
		return orient.Result{}, orient.ErrUndetected
	case w > img.Bounds().Dy():
		return orient.Result{Rotate: 90, Confidence: 9}, nil
	}
	return orient.Result{Rotate: 0, Confidence: 9}, nil
}

func scanWith(t *testing.T, m *Manager, store *storage.Store, autoRotate bool) storage.JobMeta {
	t.Helper()
	id, err := m.StartScanWith(Options{AutoRotate: autoRotate})
	if err != nil {
		t.Fatal(err)
	}
	waitForJobDone(t, m)
	meta, err := store.LoadMeta(id)
	if err != nil {
		t.Fatal(err)
	}
	return meta
}

func newRotateManager(t *testing.T, sess *fakeSession) (*Manager, *storage.Store) {
	t.Helper()
	m, store := newTestManager(t, &fakeDriver{info: driver.Info{Name: "x"}, session: sess})
	return m, store
}

func TestAutoRotate_TurnsSidewaysPagesAfterTheScannerIsReleased(t *testing.T) {
	sess := &fakeSession{pages: []driver.Page{
		{Front: testImage(40, 20, color.NRGBA{255, 0, 0, 255})}, // sideways
		{Front: testImage(20, 40, color.NRGBA{0, 255, 0, 255})}, // upright
		{Front: testImage(7, 5, color.NRGBA{0, 0, 255, 255})},   // unreadable
	}}
	m, store := newRotateManager(t, sess)
	var closedWhenDetecting []bool
	m.WithOrienter(func(img image.Image) (orient.Result, error) {
		closedWhenDetecting = append(closedWhenDetecting, sess.closed)
		return detectByWidth(img)
	})

	meta := scanWith(t, m, store, true)

	if !meta.AutoRotate {
		t.Error("the scan should record that auto-rotate was on")
	}
	if len(meta.Pages) != 3 {
		t.Fatalf("pages = %d", len(meta.Pages))
	}
	if p := meta.Pages[0]; p.AutoRotated != 90 || p.WidthPx != 20 || p.HeightPx != 40 {
		t.Errorf("sideways page = %+v, want turned 90 to 20x40", p)
	}
	if p := meta.Pages[1]; p.AutoRotated != 0 || p.WidthPx != 20 {
		t.Errorf("upright page changed: %+v", p)
	}
	if p := meta.Pages[2]; p.AutoRotated != 0 || p.WidthPx != 7 {
		t.Errorf("unreadable page changed: %+v", p)
	}
	for i, closed := range closedWhenDetecting {
		if !closed {
			t.Errorf("detection %d ran while the scanner session was still open", i)
		}
	}

	data, _ := store.LoadPageFile(meta.ID, meta.Pages[0].File)
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil || img.Bounds().Dx() != 20 || img.Bounds().Dy() != 40 {
		t.Errorf("stored image was not rewritten upright: %v %v", img.Bounds(), err)
	}
}

func TestAutoRotate_OffByDefaultAndWithoutADetector(t *testing.T) {
	page := func() []driver.Page { return []driver.Page{{Front: testImage(40, 20, color.NRGBA{1, 2, 3, 255})}} }

	m, store := newRotateManager(t, &fakeSession{pages: page()})
	m.WithOrienter(detectByWidth)
	if meta := scanWith(t, m, store, false); meta.AutoRotate || meta.Pages[0].AutoRotated != 0 || meta.Pages[0].WidthPx != 40 {
		t.Errorf("rotated although not asked: %+v", meta)
	}

	m, store = newRotateManager(t, &fakeSession{pages: page()})
	if m.CanAutoRotate() {
		t.Error("CanAutoRotate without a detector")
	}
	if meta := scanWith(t, m, store, true); meta.AutoRotate || meta.Pages[0].WidthPx != 40 {
		t.Errorf("asked for auto-rotate with no detector: %+v", meta)
	}
}

func TestAutoRotate_StopsOnlyWhenTheToolIsMissing(t *testing.T) {
	store := storage.New(t.TempDir())
	meta := storage.JobMeta{ID: "j", Pages: []storage.PageMeta{{Index: 1, File: "page-001.png", WidthPx: 40, HeightPx: 20}}}
	if err := store.CreateJob(meta); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, testImage(40, 20, color.NRGBA{1, 2, 3, 255}))
	_ = store.SavePageFile("j", "page-001.png", buf.Bytes())

	missing := func(image.Image) (orient.Result, error) { return orient.Result{}, orient.ErrToolNotAvailable }
	if _, err := AutoRotate(store, missing, &meta); !errors.Is(err, orient.ErrToolNotAvailable) {
		t.Errorf("err = %v, want ErrToolNotAvailable", err)
	}
	broken := func(image.Image) (orient.Result, error) { return orient.Result{}, errors.New("boom") }
	if n, err := AutoRotate(store, broken, &meta); err != nil || n != 0 {
		t.Errorf("a failing page: n=%d err=%v, want it skipped", n, err)
	}
}

func TestAutoRotate_SkipsUnreadableStoredPages(t *testing.T) {
	store := storage.New(t.TempDir())
	meta := storage.JobMeta{ID: "j", Pages: []storage.PageMeta{
		{Index: 1, File: "gone.png"},
		{Index: 2, File: "page-002.png"},
	}}
	if err := store.CreateJob(meta); err != nil {
		t.Fatal(err)
	}
	_ = store.SavePageFile("j", "page-002.png", []byte("not a png"))
	called := false
	n, err := AutoRotate(store, func(image.Image) (orient.Result, error) { called = true; return orient.Result{Rotate: 90}, nil }, &meta)
	if err != nil || n != 0 || called {
		t.Errorf("n=%d err=%v called=%v, want pages skipped without detecting", n, err, called)
	}
}

func TestAutoRotate_SkipsAPageItCannotSave(t *testing.T) {
	store := storage.New(t.TempDir())
	meta := storage.JobMeta{ID: "j", Pages: []storage.PageMeta{{Index: 1, File: "page-001.png", WidthPx: 40, HeightPx: 20}}}
	_ = store.CreateJob(meta)
	var buf bytes.Buffer
	_ = png.Encode(&buf, testImage(40, 20, color.NRGBA{1, 2, 3, 255}))
	_ = store.SavePageFile("j", "page-001.png", buf.Bytes())

	old := pngEncode
	pngEncode = func(io.Writer, image.Image) error { return errors.New("encode") }
	defer func() { pngEncode = old }()
	if n, err := AutoRotate(store, detectByWidth, &meta); err != nil || n != 0 || meta.Pages[0].AutoRotated != 0 {
		t.Errorf("encode failure: n=%d err=%v page=%+v", n, err, meta.Pages[0])
	}
}

// stageSession reports the manager's stage whenever the manager calls it,
// so a test can see what the UI would have been told at each step.
type stageSession struct {
	m      *Manager
	pages  []driver.Page
	idx    int
	stages *[]string
}

func (s *stageSession) note(call string) {
	cur := s.m.CurrentJob()
	*s.stages = append(*s.stages, fmt.Sprintf("%s:%s/%d/%d", call, cur.Stage, cur.StageDone, cur.StageTotal))
}

func (s *stageSession) HasNextPage(context.Context) (bool, error) {
	s.note("hasNext")
	return s.idx < len(s.pages), nil
}

func (s *stageSession) ScanPage(context.Context, driver.ScanOptions) (driver.Page, error) {
	s.note("scan")
	s.idx++
	return s.pages[s.idx-1], nil
}

func (s *stageSession) Close() error { s.note("close"); return nil }

func TestManager_ReportsWhatItIsDoingAtEachStep(t *testing.T) {
	var stages []string
	sess := &stageSession{stages: &stages, pages: []driver.Page{
		{Front: testImage(40, 20, color.NRGBA{1, 2, 3, 255})},
		{Front: testImage(20, 40, color.NRGBA{4, 5, 6, 255})},
	}}
	store := storage.New(t.TempDir())
	m := NewManager(&fakeDriver{info: driver.Info{Name: "x"}, session: nil}, store)
	m.drv = &stageDriver{sess: sess}
	sess.m = m
	m.WithOrienter(func(img image.Image) (orient.Result, error) {
		cur := m.CurrentJob()
		stages = append(stages, fmt.Sprintf("detect:%s/%d/%d", cur.Stage, cur.StageDone, cur.StageTotal))
		return orient.Result{}, orient.ErrUndetected
	})

	if _, err := m.StartScanWith(Options{AutoRotate: true}); err != nil {
		t.Fatal(err)
	}
	final := waitForJobDone(t, m)

	want := []string{
		"hasNext:checking/0/0", "scan:scanning/1/0",
		"hasNext:checking/0/0", "scan:scanning/2/0",
		"hasNext:checking/0/0",
		"close:releasing/0/0",
		"detect:rotating/1/2", "detect:rotating/2/2",
	}
	if strings.Join(stages, " ") != strings.Join(want, " ") {
		t.Errorf("stages:\n got %v\nwant %v", stages, want)
	}
	if final.Stage != "" || final.StageDone != 0 || final.StageTotal != 0 {
		t.Errorf("a finished job still reports a stage: %+v", final)
	}
}

type stageDriver struct{ sess *stageSession }

func (d *stageDriver) Info() driver.Info                            { return driver.Info{Name: "x"} }
func (d *stageDriver) Detect(context.Context) error                 { return nil }
func (d *stageDriver) Open(context.Context) (driver.Session, error) { return d.sess, nil }

func TestManager_SetStageIgnoresAnotherJob(t *testing.T) {
	m, _ := newTestManager(t, &fakeDriver{session: &fakeSession{}})
	m.setStage("nothing-running", StageSaving, 1, 1) // no job at all: must not panic
	m.running = &JobState{ID: "a"}
	m.setStage("b", StageSaving, 1, 1)
	if m.running.Stage != "" {
		t.Errorf("another job's stage leaked: %+v", m.running)
	}
}

func TestAutoRotate_IgnoresAnAnswerThatIsNotAQuarterTurn(t *testing.T) {
	store := storage.New(t.TempDir())
	meta := storage.JobMeta{ID: "j", Pages: []storage.PageMeta{{Index: 1, File: "page-001.png", WidthPx: 40, HeightPx: 20}}}
	_ = store.CreateJob(meta)
	_ = store.SavePageFile("j", "page-001.png", pngBytes(t, testImage(40, 20, color.NRGBA{1, 2, 3, 255})))
	odd := func(image.Image) (orient.Result, error) { return orient.Result{Rotate: 45, Confidence: 9}, nil }
	if n, err := AutoRotate(store, odd, &meta); err != nil || n != 0 || meta.Pages[0].AutoRotated != 0 {
		t.Errorf("n=%d err=%v page=%+v: a 45° answer should be ignored", n, err, meta.Pages[0])
	}
}

func TestManager_OrienterIsWhatWasGiven(t *testing.T) {
	m, _ := newTestManager(t, &fakeDriver{session: &fakeSession{}})
	if m.Orienter() != nil || m.CanAutoRotate() {
		t.Error("a new manager has no detector")
	}
	m.WithOrienter(detectByWidth)
	if m.Orienter() == nil || !m.CanAutoRotate() {
		t.Error("the detector given to WithOrienter should be reported")
	}
}
