package api

import (
	"bytes"
	"image/color"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"testing/fstest"

	"github.com/andiapps-dev/doxie-scanner/internal/driver"
	"github.com/andiapps-dev/doxie-scanner/internal/scanjobs"
	"github.com/andiapps-dev/doxie-scanner/internal/storage"
)

// These tests exercise the harder-to-reach error branches across the
// handlers: storage-layer failures (missing files referenced by stale
// metadata, permission-denied writes) and the one "back side requested
// but absent" branch shared by resolvePageFile.

func skipIfRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("running as root: permission-denied simulation doesn't apply")
	}
}

func TestNewServer_ServesStaticFS(t *testing.T) {
	drv := &fakeDriver{info: driver.Info{Name: "doxie-dx400"}}
	store := storage.New(t.TempDir())
	mgr := scanjobs.NewManager(drv, store)
	fsys := fstest.MapFS{"index.html": {Data: []byte("hello")}}
	srv := NewServer(drv, mgr, store, fsys, "test")

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "hello" {
		t.Fatalf("status=%d body=%q", rec.Code, rec.Body.String())
	}
}

func TestHandleRotatePage_LoadPageFileError(t *testing.T) {
	drv := &fakeDriver{info: driver.Info{Name: "doxie-dx400"}}
	srv, store := newTestServer(t, drv)
	// Metadata references a page file that was never actually written.
	seedJob(t, store, "job-1", []storage.PageMeta{{Index: 1, File: "page-001.png"}})

	rec := httptest.NewRecorder()
	body := bytes.NewBufferString(`{"degrees":90}`)
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/scans/job-1/pages/1/rotate", body))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

func TestHandleRotatePage_SavePageFileError(t *testing.T) {
	skipIfRoot(t)
	drv := &fakeDriver{info: driver.Info{Name: "doxie-dx400"}}
	srv, store := newTestServer(t, drv)
	seedJob(t, store, "job-1", []storage.PageMeta{{Index: 1, File: "page-001.png"}})
	if err := store.SavePageFile("job-1", "page-001.png", testPNGBytes(t, 10, 10, color.NRGBA{A: 255})); err != nil {
		t.Fatal(err)
	}
	pagePath := store.Root() + "/jobs/job-1/pages/page-001.png"
	if err := os.Chmod(pagePath, 0o444); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(pagePath, 0o644)

	rec := httptest.NewRecorder()
	body := bytes.NewBufferString(`{"degrees":90}`)
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/scans/job-1/pages/1/rotate", body))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500, body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleRotatePage_SaveMetaError(t *testing.T) {
	skipIfRoot(t)
	drv := &fakeDriver{info: driver.Info{Name: "doxie-dx400"}}
	srv, store := newTestServer(t, drv)
	seedJob(t, store, "job-1", []storage.PageMeta{{Index: 1, File: "page-001.png"}})
	if err := store.SavePageFile("job-1", "page-001.png", testPNGBytes(t, 10, 10, color.NRGBA{A: 255})); err != nil {
		t.Fatal(err)
	}
	metaPath := store.Root() + "/jobs/job-1/meta.json"
	if err := os.Chmod(metaPath, 0o444); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(metaPath, 0o644)

	rec := httptest.NewRecorder()
	body := bytes.NewBufferString(`{"degrees":90}`)
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/scans/job-1/pages/1/rotate", body))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500, body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleExportPage_LoadPageFileError(t *testing.T) {
	drv := &fakeDriver{info: driver.Info{Name: "doxie-dx400"}}
	srv, store := newTestServer(t, drv)
	seedJob(t, store, "job-1", []storage.PageMeta{{Index: 1, File: "page-001.png"}})

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/scans/job-1/pages/1/export", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

func TestHandleExportPage_JPEGDecodeError(t *testing.T) {
	drv := &fakeDriver{info: driver.Info{Name: "doxie-dx400"}}
	srv, store := newTestServer(t, drv)
	seedJob(t, store, "job-1", []storage.PageMeta{{Index: 1, File: "page-001.png"}})
	if err := store.SavePageFile("job-1", "page-001.png", []byte("not a png")); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/scans/job-1/pages/1/export?format=jpg", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

func TestHandleExportPage_PDFDecodeError(t *testing.T) {
	drv := &fakeDriver{info: driver.Info{Name: "doxie-dx400"}}
	srv, store := newTestServer(t, drv)
	seedJob(t, store, "job-1", []storage.PageMeta{{Index: 1, File: "page-001.png"}})
	if err := store.SavePageFile("job-1", "page-001.png", []byte("not a png")); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/scans/job-1/pages/1/export?format=pdf", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

func TestHandleCombine_LoadPageFileError(t *testing.T) {
	drv := &fakeDriver{info: driver.Info{Name: "doxie-dx400"}}
	srv, store := newTestServer(t, drv)
	seedJob(t, store, "job-1", []storage.PageMeta{{Index: 1, File: "page-001.png"}})

	rec := httptest.NewRecorder()
	body := bytes.NewBufferString(`{"pages":[{"jobId":"job-1","page":1}]}`)
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/export/combine", body))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

func TestHandleCombine_DecodeError(t *testing.T) {
	drv := &fakeDriver{info: driver.Info{Name: "doxie-dx400"}}
	srv, store := newTestServer(t, drv)
	seedJob(t, store, "job-1", []storage.PageMeta{{Index: 1, File: "page-001.png"}})
	if err := store.SavePageFile("job-1", "page-001.png", []byte("not a png")); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	body := bytes.NewBufferString(`{"pages":[{"jobId":"job-1","page":1}]}`)
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/export/combine", body))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

func TestHandleStartScan_CreateJobError(t *testing.T) {
	skipIfRoot(t)
	drv := &fakeDriver{info: driver.Info{Name: "doxie-dx400"}, session: &fakeSession{}}
	srv, store := newTestServer(t, drv)
	if err := os.Chmod(store.Root(), 0o555); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(store.Root(), 0o755)

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/scans", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500, body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleRenameScan_SaveMetaError(t *testing.T) {
	skipIfRoot(t)
	drv := &fakeDriver{info: driver.Info{Name: "doxie-dx400"}}
	srv, store := newTestServer(t, drv)
	seedJob(t, store, "job-1", nil)
	metaPath := store.Root() + "/jobs/job-1/meta.json"
	if err := os.Chmod(metaPath, 0o444); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(metaPath, 0o644)

	rec := httptest.NewRecorder()
	body := bytes.NewBufferString(`{"name":"new name"}`)
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/api/scans/job-1", body))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500, body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleDeleteScan_DeleteJobError(t *testing.T) {
	skipIfRoot(t)
	drv := &fakeDriver{info: driver.Info{Name: "doxie-dx400"}}
	srv, store := newTestServer(t, drv)
	seedJob(t, store, "job-1", nil)
	// Removing write permission from the *parent* jobs/ directory (rather
	// than job-1 itself) lets LoadMeta still succeed — it only needs
	// read+execute to traverse down to meta.json — while os.RemoveAll
	// fails because unlinking the job-1 entry requires write on jobs/.
	jobsDir := store.Root() + "/jobs"
	if err := os.Chmod(jobsDir, 0o555); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(jobsDir, 0o755)

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/scans/job-1", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500, body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleReorderPages_SaveMetaError(t *testing.T) {
	skipIfRoot(t)
	drv := &fakeDriver{info: driver.Info{Name: "doxie-dx400"}}
	srv, store := newTestServer(t, drv)
	seedJob(t, store, "job-1", []storage.PageMeta{
		{Index: 1, File: "page-001.png"},
		{Index: 2, File: "page-002.png"},
	})
	metaPath := store.Root() + "/jobs/job-1/meta.json"
	if err := os.Chmod(metaPath, 0o444); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(metaPath, 0o644)

	rec := httptest.NewRecorder()
	body := bytes.NewBufferString(`{"order":[2,1]}`)
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/api/scans/job-1/pages/order", body))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500, body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleGetPage_LoadPageFileError(t *testing.T) {
	drv := &fakeDriver{info: driver.Info{Name: "doxie-dx400"}}
	srv, store := newTestServer(t, drv)
	seedJob(t, store, "job-1", []storage.PageMeta{{Index: 1, File: "page-001.png"}})

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/scans/job-1/pages/1", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

func TestHandleDeletePage_DeletePageFileError(t *testing.T) {
	drv := &fakeDriver{info: driver.Info{Name: "doxie-dx400"}}
	srv, store := newTestServer(t, drv)
	// meta references a page file that was never written, so DeletePageFile fails.
	seedJob(t, store, "job-1", []storage.PageMeta{{Index: 1, File: "page-001.png"}})

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/scans/job-1/pages/1", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

func TestHandleDeletePage_SaveMetaError(t *testing.T) {
	skipIfRoot(t)
	drv := &fakeDriver{info: driver.Info{Name: "doxie-dx400"}}
	srv, store := newTestServer(t, drv)
	seedJob(t, store, "job-1", []storage.PageMeta{{Index: 1, File: "page-001.png"}})
	if err := store.SavePageFile("job-1", "page-001.png", testPNGBytes(t, 5, 5, color.NRGBA{A: 255})); err != nil {
		t.Fatal(err)
	}
	metaPath := store.Root() + "/jobs/job-1/meta.json"
	if err := os.Chmod(metaPath, 0o444); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(metaPath, 0o644)

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/scans/job-1/pages/1", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500, body=%s", rec.Code, rec.Body.String())
	}
}
