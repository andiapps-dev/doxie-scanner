package api

import (
	"encoding/json"
	"image"
	"image/color"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/andiapps-dev/doxie-scanner/internal/driver"
	"github.com/andiapps-dev/doxie-scanner/internal/orient"
	"github.com/andiapps-dev/doxie-scanner/internal/scanjobs"
	"github.com/andiapps-dev/doxie-scanner/internal/storage"
)

func sidewaysIfWide(img image.Image) (orient.Result, error) {
	if img.Bounds().Dx() > img.Bounds().Dy() {
		return orient.Result{Rotate: 270, Confidence: 8}, nil
	}
	return orient.Result{}, orient.ErrUndetected
}

func postAutoRotate(srv *Server, id string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/scans/"+id+"/auto-rotate", nil))
	return rec
}

func seedTwoPageJob(t *testing.T, store *storage.Store, id string) {
	t.Helper()
	seedJob(t, store, id, []storage.PageMeta{
		{Index: 1, File: "page-001.png", WidthPx: 40, HeightPx: 20},
		{Index: 2, File: "page-002.png", WidthPx: 20, HeightPx: 40},
	})
	_ = store.SavePageFile(id, "page-001.png", testPNGBytes(t, 40, 20, color.NRGBA{255, 0, 0, 255}))
	_ = store.SavePageFile(id, "page-002.png", testPNGBytes(t, 20, 40, color.NRGBA{0, 255, 0, 255}))
}

func TestHandleAutoRotate_TurnsPagesAndSaves(t *testing.T) {
	srv, store := newTestServer(t, &fakeDriver{info: driver.Info{Name: "d"}})
	srv.jobs.WithOrienter(sidewaysIfWide)
	seedTwoPageJob(t, store, "job-1")

	rec := postAutoRotate(srv, "job-1")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var meta storage.JobMeta
	if err := json.Unmarshal(rec.Body.Bytes(), &meta); err != nil {
		t.Fatal(err)
	}
	if p := meta.Pages[0]; p.AutoRotated != 270 || p.WidthPx != 20 || p.HeightPx != 40 {
		t.Errorf("page 1 = %+v", p)
	}
	if p := meta.Pages[1]; p.AutoRotated != 0 || p.WidthPx != 20 {
		t.Errorf("page 2 changed: %+v", p)
	}
	saved, _ := store.LoadMeta("job-1")
	if saved.Pages[0].AutoRotated != 270 {
		t.Error("the result was not saved")
	}
}

func TestHandleAutoRotate_Refusals(t *testing.T) {
	srv, store := newTestServer(t, &fakeDriver{info: driver.Info{Name: "d"}})
	seedTwoPageJob(t, store, "job-1")

	if rec := postAutoRotate(srv, "job-1"); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("no detector: status = %d, want 503", rec.Code)
	}

	srv.jobs.WithOrienter(sidewaysIfWide)
	if rec := postAutoRotate(srv, "nope"); rec.Code != http.StatusNotFound {
		t.Errorf("unknown scan: status = %d, want 404", rec.Code)
	}

	srv.jobs.WithOrienter(func(image.Image) (orient.Result, error) { return orient.Result{}, orient.ErrToolNotAvailable })
	if rec := postAutoRotate(srv, "job-1"); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("tool vanished: status = %d, want 503", rec.Code)
	}
}

func TestHandleAutoRotate_RefusedWhileScanning(t *testing.T) {
	drv := &fakeDriver{info: driver.Info{Name: "d"}, session: &fakeSession{block: make(chan struct{})}}
	srv, store := newTestServer(t, drv)
	srv.jobs.WithOrienter(sidewaysIfWide)
	id, err := srv.jobs.StartScan(false)
	if err != nil {
		t.Fatal(err)
	}
	_ = store // the running job's metadata is in the store already
	if rec := postAutoRotate(srv, id); rec.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409", rec.Code)
	}
}

func TestHandleStartScan_AutoRotateFlagReachesTheManager(t *testing.T) {
	sess := &fakeSession{pages: []driver.Page{{Front: testImage(40, 20, color.NRGBA{1, 2, 3, 255})}}}
	drv := &fakeDriver{info: driver.Info{Name: "d"}, session: sess}
	store := storage.New(t.TempDir())
	mgr := scanjobs.NewManager(drv, store).WithOrienter(sidewaysIfWide)
	srv := NewServer(drv, mgr, store, nil, "test", "eng")

	rec := httptest.NewRecorder()
	body := `{"duplex":false,"autoRotate":true}`
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/scans", stringsReader(body)))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var resp startScanResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	for mgr.CurrentJob().Status == storage.StatusRunning {
		time.Sleep(time.Millisecond)
	}
	meta, _ := store.LoadMeta(resp.JobID)
	if !meta.AutoRotate || meta.Pages[0].AutoRotated != 270 {
		t.Errorf("meta = %+v", meta)
	}
}

func TestHandleScannerStatus_ReportsFeatures(t *testing.T) {
	srv, _ := newTestServer(t, &fakeDriver{info: driver.Info{Name: "d"}})
	get := func() scannerStatusResponse {
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/scanner/status", nil))
		var r scannerStatusResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &r)
		return r
	}
	if r := get(); r.AutoRotate || r.PaperlessURL != "" {
		t.Errorf("features on by default: %+v", r)
	}
	srv.jobs.WithOrienter(sidewaysIfWide)
	srv.paperless = newFakePaperless(t, nil).client
	if r := get(); !r.AutoRotate || r.PaperlessURL == "" {
		t.Errorf("features not reported: %+v", r)
	}
}

func TestHandleAutoRotate_SavesThePagesItTurnedWhenTheToolVanishesPartWay(t *testing.T) {
	srv, store := newTestServer(t, &fakeDriver{info: driver.Info{Name: "d"}})
	seedTwoPageJob(t, store, "job-1") // page 1 is sideways (wide), page 2 upright
	// Turns page 1, then tesseract "disappears" on page 2.
	srv.jobs.WithOrienter(func(img image.Image) (orient.Result, error) {
		if img.Bounds().Dx() > img.Bounds().Dy() {
			return orient.Result{Rotate: 90, Confidence: 9}, nil
		}
		return orient.Result{}, orient.ErrToolNotAvailable
	})

	rec := postAutoRotate(srv, "job-1")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	saved, _ := store.LoadMeta("job-1")
	if p := saved.Pages[0]; p.AutoRotated != 90 || p.WidthPx != 20 || p.HeightPx != 40 {
		t.Errorf("page 1 was rewritten on disk but its metadata says %+v", p)
	}
}
