package api

import (
	"bytes"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/andiapps-dev/doxie-scanner/internal/driver"
	"github.com/andiapps-dev/doxie-scanner/internal/storage"
)

func TestHandleRotatePage_UpdatesFrontDimensions(t *testing.T) {
	drv := &fakeDriver{info: driver.Info{Name: "doxie-dx400"}}
	srv, store := newTestServer(t, drv)
	seedJob(t, store, "job-1", []storage.PageMeta{
		{Index: 1, File: "page-001.png", WidthPx: 10, HeightPx: 20},
	})
	if err := store.SavePageFile("job-1", "page-001.png", testPNGBytes(t, 10, 20, color.NRGBA{R: 255, A: 255})); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	body := bytes.NewBufferString(`{"degrees":90}`)
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/scans/job-1/pages/1/rotate", body))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204, body=%s", rec.Code, rec.Body.String())
	}

	meta, err := store.LoadMeta("job-1")
	if err != nil {
		t.Fatalf("LoadMeta: %v", err)
	}
	if meta.Pages[0].WidthPx != 20 || meta.Pages[0].HeightPx != 10 {
		t.Errorf("expected dimensions swapped after 90deg rotation, got %+v", meta.Pages[0])
	}

	data, err := store.LoadPageFile("job-1", "page-001.png")
	if err != nil {
		t.Fatalf("LoadPageFile: %v", err)
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode rotated PNG: %v", err)
	}
	if img.Bounds().Dx() != 20 || img.Bounds().Dy() != 10 {
		t.Errorf("stored image not actually rotated: bounds=%v", img.Bounds())
	}
}

func TestHandleRotatePage_OnlyAffectsThatPage(t *testing.T) {
	drv := &fakeDriver{info: driver.Info{Name: "doxie-dx400"}}
	srv, store := newTestServer(t, drv)
	seedJob(t, store, "job-1", []storage.PageMeta{
		{Index: 1, File: "page-001.png", WidthPx: 10, HeightPx: 20},
		{Index: 2, File: "page-002.png", WidthPx: 10, HeightPx: 20},
	})
	for _, f := range []string{"page-001.png", "page-002.png"} {
		if err := store.SavePageFile("job-1", f, testPNGBytes(t, 10, 20, color.NRGBA{A: 255})); err != nil {
			t.Fatal(err)
		}
	}

	rec := httptest.NewRecorder()
	body := bytes.NewBufferString(`{"degrees":90}`)
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/scans/job-1/pages/2/rotate", body))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204, body=%s", rec.Code, rec.Body.String())
	}

	meta, err := store.LoadMeta("job-1")
	if err != nil {
		t.Fatalf("LoadMeta: %v", err)
	}
	if meta.Pages[0].WidthPx != 10 || meta.Pages[0].HeightPx != 20 {
		t.Errorf("page 1 should be untouched by rotating page 2, got %+v", meta.Pages[0])
	}
	if meta.Pages[1].WidthPx != 20 || meta.Pages[1].HeightPx != 10 {
		t.Errorf("page 2 should be rotated, got %+v", meta.Pages[1])
	}
}

func TestHandleRotatePage_InvalidDegrees(t *testing.T) {
	drv := &fakeDriver{info: driver.Info{Name: "doxie-dx400"}}
	srv, store := newTestServer(t, drv)
	seedJob(t, store, "job-1", []storage.PageMeta{{Index: 1, File: "page-001.png"}})
	if err := store.SavePageFile("job-1", "page-001.png", testPNGBytes(t, 10, 10, color.NRGBA{A: 255})); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	body := bytes.NewBufferString(`{"degrees":45}`)
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/scans/job-1/pages/1/rotate", body))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestHandleRotatePage_BadJSON(t *testing.T) {
	drv := &fakeDriver{info: driver.Info{Name: "doxie-dx400"}}
	srv, _ := newTestServer(t, drv)

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/scans/job-1/pages/1/rotate", bytes.NewBufferString("not json")))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestHandleRotatePage_PageNotFound(t *testing.T) {
	drv := &fakeDriver{info: driver.Info{Name: "doxie-dx400"}}
	srv, store := newTestServer(t, drv)
	seedJob(t, store, "job-1", nil)

	rec := httptest.NewRecorder()
	body := bytes.NewBufferString(`{"degrees":90}`)
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/scans/job-1/pages/1/rotate", body))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestHandleRotatePage_StoredFileNotAPNG(t *testing.T) {
	drv := &fakeDriver{info: driver.Info{Name: "doxie-dx400"}}
	srv, store := newTestServer(t, drv)
	seedJob(t, store, "job-1", []storage.PageMeta{{Index: 1, File: "page-001.png"}})
	if err := store.SavePageFile("job-1", "page-001.png", []byte("not a png")); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	body := bytes.NewBufferString(`{"degrees":90}`)
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/scans/job-1/pages/1/rotate", body))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

func TestHandleCropPage(t *testing.T) {
	drv := &fakeDriver{info: driver.Info{Name: "doxie-dx400"}}
	srv, store := newTestServer(t, drv)
	seedJob(t, store, "job-1", []storage.PageMeta{
		{Index: 1, File: "page-001.png", WidthPx: 20, HeightPx: 20},
	})
	if err := store.SavePageFile("job-1", "page-001.png", testPNGBytes(t, 20, 20, color.NRGBA{R: 255, A: 255})); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	body := bytes.NewBufferString(`{"x":0,"y":0,"width":10,"height":5}`)
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/scans/job-1/pages/1/crop", body))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204, body=%s", rec.Code, rec.Body.String())
	}

	meta, err := store.LoadMeta("job-1")
	if err != nil {
		t.Fatalf("LoadMeta: %v", err)
	}
	if meta.Pages[0].WidthPx != 10 || meta.Pages[0].HeightPx != 5 {
		t.Errorf("expected cropped dimensions 10x5, got %+v", meta.Pages[0])
	}
}

func TestHandleCropPage_OutOfBounds(t *testing.T) {
	drv := &fakeDriver{info: driver.Info{Name: "doxie-dx400"}}
	srv, store := newTestServer(t, drv)
	seedJob(t, store, "job-1", []storage.PageMeta{{Index: 1, File: "page-001.png"}})
	if err := store.SavePageFile("job-1", "page-001.png", testPNGBytes(t, 10, 10, color.NRGBA{A: 255})); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	body := bytes.NewBufferString(`{"x":100,"y":100,"width":10,"height":10}`)
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/scans/job-1/pages/1/crop", body))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestHandleCropPage_BadJSON(t *testing.T) {
	drv := &fakeDriver{info: driver.Info{Name: "doxie-dx400"}}
	srv, _ := newTestServer(t, drv)

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/scans/job-1/pages/1/crop", bytes.NewBufferString("not json")))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestHandleCropPage_InvalidPageNumber(t *testing.T) {
	drv := &fakeDriver{info: driver.Info{Name: "doxie-dx400"}}
	srv, _ := newTestServer(t, drv)

	rec := httptest.NewRecorder()
	body := bytes.NewBufferString(`{"x":0,"y":0,"width":1,"height":1}`)
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/scans/job-1/pages/abc/crop", body))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestHandleCropPage_JobNotFound(t *testing.T) {
	drv := &fakeDriver{info: driver.Info{Name: "doxie-dx400"}}
	srv, _ := newTestServer(t, drv)

	rec := httptest.NewRecorder()
	body := bytes.NewBufferString(`{"x":0,"y":0,"width":1,"height":1}`)
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/scans/nope/pages/1/crop", body))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}
