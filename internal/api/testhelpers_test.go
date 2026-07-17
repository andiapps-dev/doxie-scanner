package api

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math/rand"
	"testing"
	"time"

	"github.com/andiapps-dev/doxie-scanner/internal/driver"
	"github.com/andiapps-dev/doxie-scanner/internal/scanjobs"
	"github.com/andiapps-dev/doxie-scanner/internal/storage"
)

// newTestServer builds a Server wired against a real storage.Store
// backed by t.TempDir() (no reason to fake the filesystem too) and a
// scanjobs.Manager wrapping drv.
func newTestServer(t *testing.T, drv driver.Driver) (*Server, *storage.Store) {
	t.Helper()
	store := storage.New(t.TempDir())
	mgr := scanjobs.NewManager(drv, store)
	return NewServer(drv, mgr, store, nil, "test"), store
}

func testImage(w, h int, c color.NRGBA) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	return img
}

func testPNGBytes(t *testing.T, w, h int, c color.NRGBA) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, testImage(w, h, c)); err != nil {
		t.Fatalf("encode test PNG: %v", err)
	}
	return buf.Bytes()
}

// noisyPNGBytes builds a smooth gradient with per-pixel jitter (seeded
// math/rand, so this stays deterministic) rather than a flat color — a
// flat color's JPEG encoding barely changes size across quality levels
// (there's no AC coefficient variation for quality to act on), which
// would make a quality-comparison test meaningless or flaky. See the
// equivalent helper (and its longer explanation) in
// internal/pdfexport/pdfexport_test.go.
func noisyPNGBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	rng := rand.New(rand.NewSource(42))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			jitter := rng.Intn(31) - 15
			clamp := func(v int) uint8 {
				if v < 0 {
					return 0
				}
				if v > 255 {
					return 255
				}
				return uint8(v)
			}
			img.Set(x, y, color.NRGBA{
				R: clamp(255*x/w + jitter),
				G: clamp(255*y/h + jitter),
				B: clamp(200 + jitter),
				A: 255,
			})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode noisy test PNG: %v", err)
	}
	return buf.Bytes()
}

// seedJob creates a job directly in the store (bypassing any real scan)
// with the given pages, useful for testing every handler except
// StartScan itself.
func seedJob(t *testing.T, store *storage.Store, id string, pages []storage.PageMeta) storage.JobMeta {
	t.Helper()
	meta := storage.JobMeta{
		ID:        id,
		Name:      "Scan " + id,
		Driver:    "fake-driver",
		CreatedAt: time.Now(),
		Status:    storage.StatusCompleted,
		PageCount: len(pages),
		Pages:     pages,
	}
	if err := store.CreateJob(meta); err != nil {
		t.Fatalf("seedJob: CreateJob: %v", err)
	}
	return meta
}
