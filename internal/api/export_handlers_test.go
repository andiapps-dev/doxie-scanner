package api

import (
	"bytes"
	"image/color"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/andiapps-dev/doxie-scanner/internal/driver"
	"github.com/andiapps-dev/doxie-scanner/internal/storage"
)

func seedPageJob(t *testing.T, store *storage.Store, id string) {
	t.Helper()
	seedJob(t, store, id, []storage.PageMeta{{Index: 1, File: "page-001.png", WidthPx: 12, HeightPx: 8}})
	if err := store.SavePageFile(id, "page-001.png", testPNGBytes(t, 12, 8, color.NRGBA{R: 255, A: 255})); err != nil {
		t.Fatal(err)
	}
}

func TestHandleExportPage_PNG(t *testing.T) {
	drv := &fakeDriver{info: driver.Info{Name: "doxie-dx400"}}
	srv, store := newTestServer(t, drv)
	seedPageJob(t, store, "job-1")

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/scans/job-1/pages/1/export?format=png", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/png" {
		t.Errorf("Content-Type = %q", ct)
	}
}

func TestHandleExportPage_DefaultFormatIsPNG(t *testing.T) {
	drv := &fakeDriver{info: driver.Info{Name: "doxie-dx400"}}
	srv, store := newTestServer(t, drv)
	seedPageJob(t, store, "job-1")

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/scans/job-1/pages/1/export", nil))
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("status=%d content-type=%q", rec.Code, rec.Header().Get("Content-Type"))
	}
}

func TestHandleExportPage_JPEG(t *testing.T) {
	drv := &fakeDriver{info: driver.Info{Name: "doxie-dx400"}}
	srv, store := newTestServer(t, drv)
	seedPageJob(t, store, "job-1")

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/scans/job-1/pages/1/export?format=jpg", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/jpeg" {
		t.Errorf("Content-Type = %q", ct)
	}
	if !bytes.HasPrefix(rec.Body.Bytes(), []byte{0xff, 0xd8}) {
		t.Error("expected JPEG magic bytes")
	}
}

func TestHandleExportPage_PDF(t *testing.T) {
	drv := &fakeDriver{info: driver.Info{Name: "doxie-dx400"}}
	srv, store := newTestServer(t, drv)
	seedPageJob(t, store, "job-1")

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/scans/job-1/pages/1/export?format=pdf", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/pdf" {
		t.Errorf("Content-Type = %q", ct)
	}
	if !bytes.HasPrefix(rec.Body.Bytes(), []byte("%PDF")) {
		t.Error("expected PDF magic bytes")
	}
}

func TestHandleExportPage_UnsupportedFormat(t *testing.T) {
	drv := &fakeDriver{info: driver.Info{Name: "doxie-dx400"}}
	srv, store := newTestServer(t, drv)
	seedPageJob(t, store, "job-1")

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/scans/job-1/pages/1/export?format=bmp", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestHandleExportPage_JobNotFound(t *testing.T) {
	drv := &fakeDriver{info: driver.Info{Name: "doxie-dx400"}}
	srv, _ := newTestServer(t, drv)

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/scans/nope/pages/1/export", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestHandleCombine_Success(t *testing.T) {
	drv := &fakeDriver{info: driver.Info{Name: "doxie-dx400"}}
	srv, store := newTestServer(t, drv)
	seedPageJob(t, store, "job-1")
	seedPageJob(t, store, "job-2")

	rec := httptest.NewRecorder()
	body := bytes.NewBufferString(`{"pages":[{"jobId":"job-1","page":1},{"jobId":"job-2","page":1}],"title":"combined-doc"}`)
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/export/combine", body))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if !bytes.HasPrefix(rec.Body.Bytes(), []byte("%PDF")) {
		t.Error("expected PDF magic bytes")
	}
	if !bytes.Contains([]byte(rec.Header().Get("Content-Disposition")), []byte("combined-doc.pdf")) {
		t.Errorf("Content-Disposition = %q", rec.Header().Get("Content-Disposition"))
	}
}

func TestHandleCombine_DefaultTitle(t *testing.T) {
	drv := &fakeDriver{info: driver.Info{Name: "doxie-dx400"}}
	srv, store := newTestServer(t, drv)
	seedPageJob(t, store, "job-1")

	rec := httptest.NewRecorder()
	body := bytes.NewBufferString(`{"pages":[{"jobId":"job-1","page":1}]}`)
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/export/combine", body))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if !bytes.Contains([]byte(rec.Header().Get("Content-Disposition")), []byte("combined.pdf")) {
		t.Errorf("Content-Disposition = %q", rec.Header().Get("Content-Disposition"))
	}
}

func TestHandleCombine_MultiplePagesFromSameJob(t *testing.T) {
	drv := &fakeDriver{info: driver.Info{Name: "doxie-dx400"}}
	srv, store := newTestServer(t, drv)
	seedJob(t, store, "job-1", []storage.PageMeta{
		{Index: 1, File: "page-001.png"},
		{Index: 2, File: "page-002.png"},
	})
	for _, f := range []string{"page-001.png", "page-002.png"} {
		if err := store.SavePageFile("job-1", f, testPNGBytes(t, 10, 10, color.NRGBA{A: 255})); err != nil {
			t.Fatal(err)
		}
	}

	rec := httptest.NewRecorder()
	body := bytes.NewBufferString(`{"pages":[{"jobId":"job-1","page":2},{"jobId":"job-1","page":1}]}`)
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/export/combine", body))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleCombine_EmptyPages(t *testing.T) {
	drv := &fakeDriver{info: driver.Info{Name: "doxie-dx400"}}
	srv, _ := newTestServer(t, drv)

	rec := httptest.NewRecorder()
	body := bytes.NewBufferString(`{"pages":[]}`)
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/export/combine", body))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestHandleCombine_BadJSON(t *testing.T) {
	drv := &fakeDriver{info: driver.Info{Name: "doxie-dx400"}}
	srv, _ := newTestServer(t, drv)

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/export/combine", bytes.NewBufferString("not json")))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestHandleCombine_JobNotFound(t *testing.T) {
	drv := &fakeDriver{info: driver.Info{Name: "doxie-dx400"}}
	srv, _ := newTestServer(t, drv)

	rec := httptest.NewRecorder()
	body := bytes.NewBufferString(`{"pages":[{"jobId":"nope","page":1}]}`)
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/export/combine", body))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestHandleCombine_PageNotFound(t *testing.T) {
	drv := &fakeDriver{info: driver.Info{Name: "doxie-dx400"}}
	srv, store := newTestServer(t, drv)
	seedJob(t, store, "job-1", nil)

	rec := httptest.NewRecorder()
	body := bytes.NewBufferString(`{"pages":[{"jobId":"job-1","page":1}]}`)
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/export/combine", body))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}
