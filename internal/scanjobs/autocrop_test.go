package scanjobs

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/andiapps-dev/doxie-scanner/internal/driver"
	"github.com/andiapps-dev/doxie-scanner/internal/orient"
	"github.com/andiapps-dev/doxie-scanner/internal/storage"
)

var (
	scannerGray = color.NRGBA{195, 198, 197, 255}
	sheetWhite  = color.NRGBA{236, 235, 232, 255}
)

// cardScan is a w x h scan of the scanner's backing with a sheetW x sheetH
// sheet in the middle and bars of print on it, like a photo or card.
func cardScan(w, h, sheetW, sheetH int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := scannerGray
			if x >= (w-sheetW)/2 && x < (w+sheetW)/2 && y >= (h-sheetH)/2 && y < (h+sheetH)/2 {
				c = sheetWhite
				if (y-(h-sheetH)/2)%60 < 8 && x > (w-sheetW)/2+40 && x < (w+sheetW)/2-40 {
					c = color.NRGBA{40, 40, 60, 255}
				}
			}
			img.SetNRGBA(x, y, c)
		}
	}
	return img
}

func pngBytes(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// cropJob stores a scan of three pages: a card on the backing, a page that
// fills the scan, and a file that isn't an image.
func cropJob(t *testing.T) (*storage.Store, storage.JobMeta, []byte) {
	t.Helper()
	store := storage.New(t.TempDir())
	card := pngBytes(t, cardScan(1275, 940, 600, 900))
	meta := storage.JobMeta{ID: "j", Pages: []storage.PageMeta{
		{Index: 1, File: "page-001.png", WidthPx: 1275, HeightPx: 940},
		{Index: 2, File: "page-002.png", WidthPx: 1275, HeightPx: 940},
		{Index: 3, File: "page-003.png"},
		{Index: 4, File: "gone.png"},
	}}
	if err := store.CreateJob(meta); err != nil {
		t.Fatal(err)
	}
	_ = store.SavePageFile("j", "page-001.png", card)
	_ = store.SavePageFile("j", "page-002.png", pngBytes(t, cardScan(1275, 940, 1275, 940)))
	_ = store.SavePageFile("j", "page-003.png", []byte("not a png"))
	return store, meta, card
}

func TestAutoCrop_TrimsACardAndKeepsItsOriginal(t *testing.T) {
	store, meta, card := cropJob(t)
	var seen [][2]int
	n, err := AutoCropProgress(store, &meta, func(done, total int) { seen = append(seen, [2]int{done, total}) })
	if err != nil || n != 1 {
		t.Fatalf("changed %d (err %v), want just the card", n, err)
	}
	if len(seen) != 4 || seen[0] != [2]int{1, 4} || seen[3] != [2]int{4, 4} {
		t.Errorf("progress = %v", seen)
	}

	p := meta.Pages[0]
	if p.WidthPx > 650 || p.HeightPx < 880 || p.AutoCrop == nil || p.AutoCrop.FromWidth != 1275 || p.AutoCrop.FromHeight != 940 {
		t.Errorf("card page = %+v (%+v)", p, p.AutoCrop)
	}
	if p.Original != "page-001.orig.png" {
		t.Errorf("Original = %q", p.Original)
	}
	if kept, _ := store.LoadPageFile("j", p.Original); !bytes.Equal(kept, card) {
		t.Error("the kept original is not the page as scanned")
	}
	data, _ := store.LoadPageFile("j", p.File)
	if img, _ := png.Decode(bytes.NewReader(data)); img.Bounds().Dx() != p.WidthPx {
		t.Errorf("stored image is %v, metadata says width %d", img.Bounds(), p.WidthPx)
	}

	for _, i := range []int{1, 2, 3} {
		if q := meta.Pages[i]; q.Original != "" || q.AutoCrop != nil {
			t.Errorf("page %d should have been left alone: %+v", q.Index, q)
		}
	}
}

func TestAutoCrop_RunningAgainChangesNothingAndKeepsTheTrueOriginal(t *testing.T) {
	store, meta, card := cropJob(t)
	AutoCrop(store, &meta)
	again, err := AutoCrop(store, &meta)
	if err != nil || again != 0 {
		t.Fatalf("second run changed %d pages (err %v)", again, err)
	}
	if kept, _ := store.LoadPageFile("j", meta.Pages[0].Original); !bytes.Equal(kept, card) {
		t.Error("the original was overwritten by a later run")
	}
}

func TestAutoCrop_KeepsAnExistingOriginalWhenCroppingAgain(t *testing.T) {
	store, meta, _ := cropJob(t)
	// A page that already has a kept original (say it was rotated by hand
	// afterwards) but is a card again: the kept file stays the true original.
	_ = store.SavePageFile("j", "page-001.orig.png", []byte("the real original"))
	meta.Pages[0].Original = "page-001.orig.png"
	if n, _ := AutoCrop(store, &meta); n != 1 {
		t.Fatalf("changed %d", n)
	}
	if kept, _ := store.LoadPageFile("j", "page-001.orig.png"); string(kept) != "the real original" {
		t.Error("the existing original was replaced")
	}
}

func TestAutoCrop_LeavesAPageAloneIfItsOriginalCannotBeKept(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("file permissions don't bind root")
	}
	store, meta, card := cropJob(t)
	dir := filepath.Dir(store.PageFilePath("j", "x"))
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0o700)
	n, _ := AutoCrop(store, &meta)
	os.Chmod(dir, 0o700)
	if n != 0 || meta.Pages[0].Original != "" || meta.Pages[0].AutoCrop != nil {
		t.Errorf("a page was changed without its original being saved: n=%d %+v", n, meta.Pages[0])
	}
	if now, _ := store.LoadPageFile("j", "page-001.png"); !bytes.Equal(now, card) {
		t.Error("the page was rewritten")
	}
}

func TestAutoCrop_SkipsAPageItCannotEncode(t *testing.T) {
	store, meta, _ := cropJob(t)
	old := pngEncode
	pngEncode = func(io.Writer, image.Image) error { return errors.New("encode") }
	defer func() { pngEncode = old }()
	if n, _ := AutoCrop(store, &meta); n != 0 || meta.Pages[0].Original != "" {
		t.Errorf("n=%d page=%+v", n, meta.Pages[0])
	}
}

func TestManager_AutoCropRunsAfterTheScannerIsReleasedAndBeforeRotating(t *testing.T) {
	var stages []string
	sess := &stageSession{stages: &stages, pages: []driver.Page{
		{Front: cardScan(1275, 940, 600, 900)},
		{Front: cardScan(1275, 940, 1275, 940)},
	}}
	store := storage.New(t.TempDir())
	m := NewManager(&stageDriver{sess: sess}, store)
	sess.m = m
	m.WithOrienter(func(image.Image) (orient.Result, error) {
		stages = append(stages, "detect:"+m.CurrentJob().Stage)
		return orient.Result{}, orient.ErrUndetected
	})
	// Writing the cropped page is the one thing only the crop pass does with
	// a PNG after the scan, so the stage reported then is the crop stage.
	realEncode := pngEncode
	cropping := 0
	pngEncode = func(w io.Writer, img image.Image) error {
		if cur := m.CurrentJob(); cur.Stage == StageCropping {
			cropping++
			if cur.StageDone < 1 || cur.StageTotal != 2 {
				t.Errorf("cropping progress %d of %d, want 1..2 of 2", cur.StageDone, cur.StageTotal)
			}
		}
		return realEncode(w, img)
	}
	defer func() { pngEncode = realEncode }()

	id, err := m.StartScanWith(Options{AutoCrop: true, AutoRotate: true})
	if err != nil {
		t.Fatal(err)
	}
	waitForJobDone(t, m)
	meta, _ := store.LoadMeta(id)

	if !meta.AutoCrop || meta.Pages[0].AutoCrop == nil || meta.Pages[1].AutoCrop != nil {
		t.Errorf("crop results: %+v / %+v", meta.Pages[0].AutoCrop, meta.Pages[1].AutoCrop)
	}
	if cropping != 1 {
		t.Errorf("the cropping stage was reported while saving %d pages, want 1", cropping)
	}
	released := -1
	for i, s := range stages {
		if s == "close:releasing/0/0" {
			released = i
		}
	}
	if released < 0 || len(stages) < released+3 || stages[released+1] != "detect:rotating" || stages[released+2] != "detect:rotating" {
		t.Errorf("order of events = %v: want the scanner released, then both pages read for rotation", stages)
	}
}
