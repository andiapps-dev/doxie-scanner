package api

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
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
