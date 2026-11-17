package api

import (
	"bytes"
	"image/color"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/andiapps-dev/doxie-scanner/internal/driver"
	"github.com/andiapps-dev/doxie-scanner/internal/storage"
)

func getThumb(srv *Server, id string, n int) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/scans/"+id+"/pages/"+itoa(n)+"/thumb", nil))
	return rec
}

func itoa(n int) string { return string(rune('0' + n)) }

func thumbFiles(t *testing.T, store *storage.Store, id string) []string {
	t.Helper()
	entries, _ := os.ReadDir(filepath.Join(store.Root(), "jobs", id, "thumbs"))
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func TestThumb_IsASmallJPEGOfThePage(t *testing.T) {
	srv, store := newTestServer(t, &fakeDriver{info: driver.Info{Name: "d"}})
	seedJob(t, store, "j", []storage.PageMeta{{Index: 1, File: "page-001.png"}})
	_ = store.SavePageFile("j", "page-001.png", testPNGBytes(t, 1500, 2000, color.NRGBA{200, 30, 30, 255}))

	rec := getThumb(srv, "j", 1)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/jpeg" {
		t.Fatalf("status %d, type %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	img, err := jpeg.Decode(bytes.NewReader(rec.Body.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() > thumbMaxW || b.Dy() > thumbMaxH || b.Dx() < 200 {
		t.Errorf("thumbnail is %v, want it fitted into %dx%d", b, thumbMaxW, thumbMaxH)
	}
	if rec.Body.Len() > 40_000 {
		t.Errorf("thumbnail is %d bytes: not much of a saving over a 5 MB page", rec.Body.Len())
	}
	if len(thumbFiles(t, store, "j")) != 1 {
		t.Errorf("the thumbnail was not cached: %v", thumbFiles(t, store, "j"))
	}
}

func TestThumb_ComesFromTheCacheTheSecondTime(t *testing.T) {
	srv, store := newTestServer(t, &fakeDriver{info: driver.Info{Name: "d"}})
	seedJob(t, store, "j", []storage.PageMeta{{Index: 1, File: "page-001.png"}})
	_ = store.SavePageFile("j", "page-001.png", testPNGBytes(t, 600, 800, color.NRGBA{1, 2, 3, 255}))
	first := getThumb(srv, "j", 1).Body.Bytes()

	// Break the page file's contents without changing its size or time: if the
	// thumbnail were made again from it, the request would fail.
	path := store.PageFilePath("j", "page-001.png")
	info, _ := os.Stat(path)
	bad := bytes.Repeat([]byte{0}, int(info.Size()))
	_ = os.WriteFile(path, bad, 0o644)
	_ = os.Chtimes(path, info.ModTime(), info.ModTime())

	rec := getThumb(srv, "j", 1)
	if rec.Code != http.StatusOK || !bytes.Equal(rec.Body.Bytes(), first) {
		t.Errorf("second request: status %d, same bytes %v: it should have come from the cache", rec.Code, bytes.Equal(rec.Body.Bytes(), first))
	}
}

func TestThumb_AnEditedPageGetsANewThumbnailAndTheOldOneGoes(t *testing.T) {
	srv, store := newTestServer(t, &fakeDriver{info: driver.Info{Name: "d"}})
	seedJob(t, store, "j", []storage.PageMeta{{Index: 1, File: "page-001.png"}})
	_ = store.SavePageFile("j", "page-001.png", testPNGBytes(t, 600, 800, color.NRGBA{250, 0, 0, 255}))
	red := getThumb(srv, "j", 1).Body.Bytes()

	_ = store.SavePageFile("j", "page-001.png", testPNGBytes(t, 600, 800, color.NRGBA{0, 0, 250, 255}))
	later := time.Now().Add(2 * time.Second)
	_ = os.Chtimes(store.PageFilePath("j", "page-001.png"), later, later)
	blue := getThumb(srv, "j", 1)

	img, _ := jpeg.Decode(bytes.NewReader(blue.Body.Bytes()))
	r, _, b, _ := img.At(img.Bounds().Dx()/2, img.Bounds().Dy()/2).RGBA()
	if bytes.Equal(blue.Body.Bytes(), red) || r>>8 > 60 || b>>8 < 180 {
		t.Errorf("after the page changed the thumbnail is still the old one (r=%d b=%d)", r>>8, b>>8)
	}
	if got := thumbFiles(t, store, "j"); len(got) != 1 {
		t.Errorf("thumbnails = %v, want the old one removed", got)
	}
}

func TestThumb_Errors(t *testing.T) {
	srv, store := newTestServer(t, &fakeDriver{info: driver.Info{Name: "d"}})
	seedJob(t, store, "j", []storage.PageMeta{{Index: 1, File: "page-001.png"}, {Index: 2, File: "page-002.png"}, {Index: 3, File: "gone.png"}})
	_ = store.SavePageFile("j", "page-002.png", []byte("not a png"))
	_ = store.SavePageFile("j", "page-001.png", testPNGBytes(t, 20, 20, color.NRGBA{1, 1, 1, 255}))
	for name, c := range map[string]struct {
		id   string
		n    int
		want int
	}{
		"unknown scan": {"nope", 1, http.StatusNotFound},
		"unknown page": {"j", 9, http.StatusNotFound},
		"file missing": {"j", 3, http.StatusNotFound},
		"not a png":    {"j", 2, http.StatusInternalServerError},
	} {
		if rec := getThumb(srv, c.id, c.n); rec.Code != c.want {
			t.Errorf("%s: status = %d, want %d", name, rec.Code, c.want)
		}
	}
}

func TestDeletingAPageClearsTheThumbnailsSoNoneIsLeftOnAShiftedFile(t *testing.T) {
	srv, store := newTestServer(t, &fakeDriver{info: driver.Info{Name: "d"}})
	seedJob(t, store, "j", []storage.PageMeta{{Index: 1, File: "page-001.png"}, {Index: 2, File: "page-002.png"}})
	_ = store.SavePageFile("j", "page-001.png", testPNGBytes(t, 40, 40, color.NRGBA{250, 0, 0, 255}))
	_ = store.SavePageFile("j", "page-002.png", testPNGBytes(t, 40, 40, color.NRGBA{0, 0, 250, 255}))
	getThumb(srv, "j", 1)
	getThumb(srv, "j", 2)
	if len(thumbFiles(t, store, "j")) != 2 {
		t.Fatal("setup: both thumbnails should be cached")
	}
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/scans/j/pages/1", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d", rec.Code)
	}
	if got := thumbFiles(t, store, "j"); len(got) != 0 {
		t.Errorf("thumbnails left after renumbering: %v", got)
	}
	// Page 2 is now page 1: its thumbnail must be the blue one.
	img, _ := jpeg.Decode(bytes.NewReader(getThumb(srv, "j", 1).Body.Bytes()))
	if _, _, b, _ := img.At(10, 10).RGBA(); b>>8 < 180 {
		t.Error("the renumbered page shows the wrong thumbnail")
	}
}
