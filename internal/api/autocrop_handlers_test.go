package api

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/andiapps-dev/doxie-scanner/internal/driver"
	"github.com/andiapps-dev/doxie-scanner/internal/storage"
)

// cardPNG is a scan of the scanner's gray backing with a small sheet on it
// (w x h, sheet sheetW x sheetH), as a photo or card comes out.
func cardPNG(t *testing.T, w, h, sheetW, sheetH int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := color.NRGBA{195, 198, 197, 255}
			if x >= (w-sheetW)/2 && x < (w+sheetW)/2 && y >= (h-sheetH)/2 && y < (h+sheetH)/2 {
				c = color.NRGBA{236, 235, 232, 255}
				if (y-(h-sheetH)/2)%60 < 8 && x > (w-sheetW)/2+40 && x < (w+sheetW)/2-40 {
					c = color.NRGBA{40, 40, 60, 255}
				}
			}
			img.SetNRGBA(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func seedCardJob(t *testing.T, store *storage.Store, id string) []byte {
	t.Helper()
	card := cardPNG(t, 1275, 940, 600, 900)
	seedJob(t, store, id, []storage.PageMeta{
		{Index: 1, File: "page-001.png", WidthPx: 1275, HeightPx: 940},
		{Index: 2, File: "page-002.png", WidthPx: 1275, HeightPx: 940},
	})
	_ = store.SavePageFile(id, "page-001.png", card)
	_ = store.SavePageFile(id, "page-002.png", cardPNG(t, 1275, 940, 1275, 940))
	return card
}

func postTo(srv *Server, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, nil))
	return rec
}

func decodeMeta(t *testing.T, rec *httptest.ResponseRecorder) storage.JobMeta {
	t.Helper()
	var m storage.JobMeta
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body.String())
	}
	return m
}

func TestAutoCropEndpoint_TrimsTheCardOnly(t *testing.T) {
	srv, store := newTestServer(t, &fakeDriver{info: driver.Info{Name: "d"}})
	seedCardJob(t, store, "job-1")

	rec := postTo(srv, "/api/scans/job-1/auto-crop")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	m := decodeMeta(t, rec)
	if p := m.Pages[0]; p.AutoCrop == nil || p.WidthPx > 650 || p.Original != "page-001.orig.png" {
		t.Errorf("card = %+v", p)
	}
	if p := m.Pages[1]; p.AutoCrop != nil || p.Original != "" || p.WidthPx != 1275 {
		t.Errorf("a page filling the scan was changed: %+v", p)
	}
	saved, _ := store.LoadMeta("job-1")
	if saved.Pages[0].AutoCrop == nil {
		t.Error("the result was not saved")
	}
}

func TestAutoCropEndpoint_Refusals(t *testing.T) {
	srv, store := newTestServer(t, &fakeDriver{info: driver.Info{Name: "d"}})
	seedCardJob(t, store, "job-1")
	if rec := postTo(srv, "/api/scans/nope/auto-crop"); rec.Code != http.StatusNotFound {
		t.Errorf("unknown scan: %d", rec.Code)
	}
	drv := &fakeDriver{info: driver.Info{Name: "d"}, session: &fakeSession{block: make(chan struct{})}}
	busy, _ := newTestServer(t, drv)
	id, _ := busy.jobs.StartScan(false)
	if rec := postTo(busy, "/api/scans/"+id+"/auto-crop"); rec.Code != http.StatusConflict {
		t.Errorf("running scan: %d, want 409", rec.Code)
	}
}

func TestRestorePage_PutsBackTheScannedImage(t *testing.T) {
	srv, store := newTestServer(t, &fakeDriver{info: driver.Info{Name: "d"}})
	card := seedCardJob(t, store, "job-1")
	postTo(srv, "/api/scans/job-1/auto-crop")

	rec := postTo(srv, "/api/scans/job-1/pages/1/restore")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	p := decodeMeta(t, rec).Pages[0]
	if p.WidthPx != 1275 || p.HeightPx != 940 || p.Original != "" || p.AutoCrop != nil || p.AutoRotated != 0 {
		t.Errorf("restored page = %+v", p)
	}
	if now, _ := store.LoadPageFile("job-1", "page-001.png"); !bytes.Equal(now, card) {
		t.Error("the page is not byte-identical to the scan")
	}
	if _, err := store.LoadPageFile("job-1", "page-001.orig.png"); err == nil {
		t.Error("the kept copy should be gone once restored")
	}
	if rec := postTo(srv, "/api/scans/job-1/pages/1/restore"); rec.Code != http.StatusNotFound {
		t.Errorf("restoring twice: %d, want 404 (nothing to restore)", rec.Code)
	}
}

func TestRestorePage_Errors(t *testing.T) {
	srv, store := newTestServer(t, &fakeDriver{info: driver.Info{Name: "d"}})
	seedCardJob(t, store, "job-1")
	// An ordered list, not a map: the last two cases build on the crop that
	// the first of them performs.
	cases := []struct {
		name string
		path string
		want int
		prep func()
	}{
		{"bad page number", "/api/scans/job-1/pages/x/restore", http.StatusBadRequest, nil},
		{"unknown scan", "/api/scans/nope/pages/1/restore", http.StatusNotFound, nil},
		{"unknown page", "/api/scans/job-1/pages/9/restore", http.StatusNotFound, nil},
		{"never cropped", "/api/scans/job-1/pages/2/restore", http.StatusNotFound, nil},
		{"original corrupt", "/api/scans/job-1/pages/1/restore", http.StatusInternalServerError, func() {
			postTo(srv, "/api/scans/job-1/auto-crop")
			_ = store.SavePageFile("job-1", "page-001.orig.png", []byte("junk"))
		}},
		{"original is gone", "/api/scans/job-1/pages/1/restore", http.StatusInternalServerError, func() {
			_ = store.DeletePageFile("job-1", "page-001.orig.png")
		}},
	}
	for _, c := range cases {
		if c.prep != nil {
			c.prep()
		}
		if rec := postTo(srv, c.path); rec.Code != c.want {
			t.Errorf("%s: status = %d, want %d (%s)", c.name, rec.Code, c.want, rec.Body.String())
		}
	}
}

func TestStartScan_PostProcessingIsOffUnlessAskedFor(t *testing.T) {
	run := func(body string) storage.JobMeta {
		sess := &fakeSession{pages: []driver.Page{{Front: testImage(40, 20, color.NRGBA{1, 2, 3, 255})}}}
		srv, store := newTestServer(t, &fakeDriver{info: driver.Info{Name: "d"}, session: sess})
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/scans", stringsReader(body)))
		var resp startScanResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &resp)
		for srv.jobs.CurrentJob().Status == storage.StatusRunning {
			time.Sleep(time.Millisecond)
		}
		m, _ := store.LoadMeta(resp.JobID)
		return m
	}
	// A script that predates auto-crop must keep getting pages as scanned.
	if m := run(`{"duplex":true}`); m.AutoCrop {
		t.Error("auto-crop should stay off when the request doesn't ask for it")
	}
	if m := run(`{}`); m.AutoCrop {
		t.Error("auto-crop should stay off for an empty request")
	}
	if m := run(`{"autoCrop":true}`); !m.AutoCrop {
		t.Error("autoCrop:true should turn it on")
	}
	if m := run(`{"autoCrop":false}`); m.AutoCrop {
		t.Error("autoCrop:false should leave it off")
	}
}

func TestDeletePage_RenumbersOriginalsAndSentRecords(t *testing.T) {
	srv, store := newTestServer(t, &fakeDriver{info: driver.Info{Name: "d"}})
	seedJob(t, store, "job-1", nil)
	pages := []storage.PageMeta{
		{Index: 1, File: "page-001.png", Original: "page-001.orig.png"},
		{Index: 2, File: "page-002.png"},
		{Index: 3, File: "page-003.png", Original: "page-003.orig.png"},
	}
	meta, _ := store.LoadMeta("job-1")
	meta.Pages, meta.PageCount = pages, 3
	meta.Sent = []storage.SentDocument{
		{Title: "first two", Pages: []int{1, 2}},
		{Title: "last", Pages: []int{3}},
		{Title: "only the middle", Pages: []int{2}},
	}
	_ = store.SaveMeta(meta)
	for _, f := range []string{"page-001.png", "page-002.png", "page-003.png"} {
		_ = store.SavePageFile("job-1", f, []byte("img "+f))
	}
	_ = store.SavePageFile("job-1", "page-001.orig.png", []byte("orig 1"))
	_ = store.SavePageFile("job-1", "page-003.orig.png", []byte("orig 3"))

	req := httptest.NewRequest(http.MethodDelete, "/api/scans/job-1/pages/2", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}

	got, _ := store.LoadMeta("job-1")
	if len(got.Pages) != 2 || got.Pages[1].Index != 2 || got.Pages[1].Original != "page-002.orig.png" || got.Pages[0].Original != "page-001.orig.png" {
		t.Fatalf("pages = %+v", got.Pages)
	}
	if b, _ := store.LoadPageFile("job-1", "page-002.orig.png"); string(b) != "orig 3" {
		t.Errorf("page 3's original did not follow it to page 2: %q", b)
	}
	if b, _ := store.LoadPageFile("job-1", "page-001.orig.png"); string(b) != "orig 1" {
		t.Errorf("page 1's original moved: %q", b)
	}
	if _, err := store.LoadPageFile("job-1", "page-003.orig.png"); err == nil {
		t.Error("a stale original is left under the old number")
	}
	// "first two" lost page 2 and keeps page 1; "last" moved from 3 to 2; a
	// document made only of the deleted page is dropped.
	if len(got.Sent) != 2 || got.Sent[0].Title != "first two" || len(got.Sent[0].Pages) != 1 || got.Sent[0].Pages[0] != 1 ||
		got.Sent[1].Title != "last" || got.Sent[1].Pages[0] != 2 {
		t.Errorf("sent = %+v", got.Sent)
	}
}

func TestDeletePage_RemovesTheDeletedPagesOriginal(t *testing.T) {
	srv, store := newTestServer(t, &fakeDriver{info: driver.Info{Name: "d"}})
	seedCardJob(t, store, "job-1")
	postTo(srv, "/api/scans/job-1/auto-crop")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/scans/job-1/pages/1", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d", rec.Code)
	}
	if _, err := store.LoadPageFile("job-1", "page-001.orig.png"); err == nil {
		t.Error("the deleted page's original was left behind")
	}
}

func TestRemapSent_FollowsReorderedPages(t *testing.T) {
	// Pages had been reordered to [3, 1, 2]; deleting page 1 leaves [3, 2],
	// renumbered by position to 1 and 2. A plain "shift down" would be wrong.
	got := remapSent([]storage.SentDocument{{Title: "x", Pages: []int{3, 2}}}, map[int]int{3: 1, 2: 2})
	if len(got) != 1 || got[0].Pages[0] != 1 || got[0].Pages[1] != 2 {
		t.Errorf("remapped = %+v, want pages [1 2]", got)
	}
}

func TestAScanThatIsStillRunningCannotBeEdited(t *testing.T) {
	drv := &fakeDriver{info: driver.Info{Name: "d"}, session: &fakeSession{block: make(chan struct{})}}
	srv, _ := newTestServer(t, drv)
	id, err := srv.jobs.StartScan(false)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ method, path, body string }{
		{http.MethodPatch, "/api/scans/" + id, `{"name":"x"}`},
		{http.MethodPatch, "/api/scans/" + id + "/pages/order", `{"order":[1]}`},
		{http.MethodDelete, "/api/scans/" + id + "/pages/1", ``},
		{http.MethodPost, "/api/scans/" + id + "/pages/1/rotate", `{"degrees":90}`},
		{http.MethodPost, "/api/scans/" + id + "/pages/1/crop", `{"x":0,"y":0,"width":1,"height":1}`},
		{http.MethodPost, "/api/scans/" + id + "/pages/1/restore", ``},
		{http.MethodPost, "/api/scans/" + id + "/auto-crop", ``},
		{http.MethodPost, "/api/scans/" + id + "/auto-rotate", ``},
		{http.MethodDelete, "/api/scans/" + id, ``},
	} {
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, httptest.NewRequest(c.method, c.path, strings.NewReader(c.body)))
		// auto-rotate also reports 503 when no detector is set: either way it must not edit.
		if rec.Code != http.StatusConflict && !(c.path == "/api/scans/"+id+"/auto-rotate" && rec.Code == http.StatusServiceUnavailable) {
			t.Errorf("%s %s: status = %d, want 409", c.method, c.path, rec.Code)
		}
	}
}
