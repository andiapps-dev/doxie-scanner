package api

import (
	"bytes"
	"encoding/json"
	"image/color"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/andiapps-dev/doxie-scanner/internal/driver"
	"github.com/andiapps-dev/doxie-scanner/internal/storage"
)

func waitForJobDone(t *testing.T, srv *Server) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cur := srv.jobs.CurrentJob(); cur == nil || cur.Status != storage.StatusRunning {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("timed out waiting for job to finish")
}

func TestHandleStartScan_Success(t *testing.T) {
	sess := &fakeSession{pages: []driver.Page{{Front: testImage(10, 10, color.NRGBA{A: 255})}}}
	drv := &fakeDriver{info: driver.Info{Name: "doxie-dx400"}, session: sess}
	srv, store := newTestServer(t, drv)

	body := bytes.NewBufferString(`{"duplex":true}`)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/scans", body))

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202, body=%s", rec.Code, rec.Body.String())
	}
	var resp startScanResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.JobID == "" {
		t.Fatal("expected a job ID")
	}
	waitForJobDone(t, srv)

	meta, err := store.LoadMeta(resp.JobID)
	if err != nil {
		t.Fatalf("LoadMeta: %v", err)
	}
	if meta.Status != storage.StatusCompleted || meta.PageCount != 1 {
		t.Errorf("unexpected final meta: %+v", meta)
	}
}

func TestHandleStartScan_EmptyBodyDefaultsSimplex(t *testing.T) {
	sess := &fakeSession{}
	drv := &fakeDriver{info: driver.Info{Name: "doxie-dx400"}, session: sess}
	srv, _ := newTestServer(t, drv)

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/scans", nil))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202, body=%s", rec.Code, rec.Body.String())
	}
	// Wait for the background goroutine to finish before the test (and
	// its t.TempDir() cleanup) returns, so cleanup can't race the
	// goroutine's own filesystem writes.
	waitForJobDone(t, srv)
}

func TestHandleStartScan_BadJSON(t *testing.T) {
	drv := &fakeDriver{info: driver.Info{Name: "doxie-dx400"}, session: &fakeSession{}}
	srv, _ := newTestServer(t, drv)

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/scans", bytes.NewBufferString("not json")))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestHandleStartScan_AlreadyRunning(t *testing.T) {
	sess := &fakeSession{block: make(chan struct{})}
	drv := &fakeDriver{info: driver.Info{Name: "doxie-dx400"}, session: sess}
	srv, _ := newTestServer(t, drv)

	if _, err := srv.jobs.StartScan(false); err != nil {
		t.Fatalf("first StartScan: %v", err)
	}

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/scans", nil))
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409, body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleListScans(t *testing.T) {
	drv := &fakeDriver{info: driver.Info{Name: "doxie-dx400"}}
	srv, store := newTestServer(t, drv)
	seedJob(t, store, "job-1", nil)
	seedJob(t, store, "job-2", []storage.PageMeta{{Index: 1, File: "page-001.png"}})

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/scans", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var jobs []jobSummary
	if err := json.Unmarshal(rec.Body.Bytes(), &jobs); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(jobs) != 2 {
		t.Fatalf("expected 2 jobs, got %d", len(jobs))
	}
}

func TestHandleListScans_StoreError(t *testing.T) {
	drv := &fakeDriver{info: driver.Info{Name: "doxie-dx400"}}
	srv, store := newTestServer(t, drv)
	if os.Geteuid() == 0 {
		t.Skip("running as root: permission-denied simulation doesn't apply")
	}
	// Make the jobs dir exist but unreadable, so ListJobs errors.
	seedJob(t, store, "job-1", nil)
	if err := os.Chmod(store.Root()+"/jobs", 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	defer os.Chmod(store.Root()+"/jobs", 0o755)

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/scans", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

func TestHandleGetScan(t *testing.T) {
	drv := &fakeDriver{info: driver.Info{Name: "doxie-dx400"}}
	srv, store := newTestServer(t, drv)
	seedJob(t, store, "job-1", []storage.PageMeta{{Index: 1, File: "page-001.png"}})

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/scans/job-1", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	var resp jobDetailResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.ID != "job-1" || len(resp.Pages) != 1 {
		t.Errorf("unexpected response: %+v", resp)
	}
	if resp.PagesScanned != nil {
		t.Errorf("expected no live progress overlay for a non-running job")
	}
}

func TestHandleGetScan_LiveProgressOverlay(t *testing.T) {
	sess := &fakeSession{block: make(chan struct{})}
	drv := &fakeDriver{info: driver.Info{Name: "doxie-dx400"}, session: sess}
	srv, _ := newTestServer(t, drv)

	id, err := srv.jobs.StartScan(false)
	if err != nil {
		t.Fatalf("StartScan: %v", err)
	}

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/scans/"+id, nil))
	var resp jobDetailResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.PagesScanned == nil {
		t.Error("expected a live progress overlay for the running job")
	}
}

func TestHandleGetScan_NotFound(t *testing.T) {
	drv := &fakeDriver{info: driver.Info{Name: "doxie-dx400"}}
	srv, _ := newTestServer(t, drv)

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/scans/nope", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestHandleRenameScan(t *testing.T) {
	drv := &fakeDriver{info: driver.Info{Name: "doxie-dx400"}}
	srv, store := newTestServer(t, drv)
	seedJob(t, store, "job-1", nil)

	rec := httptest.NewRecorder()
	body := bytes.NewBufferString(`{"name":"Tax documents"}`)
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/api/scans/job-1", body))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}

	meta, err := store.LoadMeta("job-1")
	if err != nil {
		t.Fatalf("LoadMeta: %v", err)
	}
	if meta.Name != "Tax documents" {
		t.Errorf("Name = %q, want %q", meta.Name, "Tax documents")
	}
}

func TestHandleRenameScan_EmptyName(t *testing.T) {
	drv := &fakeDriver{info: driver.Info{Name: "doxie-dx400"}}
	srv, store := newTestServer(t, drv)
	seedJob(t, store, "job-1", nil)

	rec := httptest.NewRecorder()
	body := bytes.NewBufferString(`{"name":"   "}`)
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/api/scans/job-1", body))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestHandleRenameScan_BadJSON(t *testing.T) {
	drv := &fakeDriver{info: driver.Info{Name: "doxie-dx400"}}
	srv, store := newTestServer(t, drv)
	seedJob(t, store, "job-1", nil)

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/api/scans/job-1", bytes.NewBufferString("not json")))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestHandleRenameScan_NotFound(t *testing.T) {
	drv := &fakeDriver{info: driver.Info{Name: "doxie-dx400"}}
	srv, _ := newTestServer(t, drv)

	rec := httptest.NewRecorder()
	body := bytes.NewBufferString(`{"name":"x"}`)
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/api/scans/nope", body))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestHandleDeleteScan(t *testing.T) {
	drv := &fakeDriver{info: driver.Info{Name: "doxie-dx400"}}
	srv, store := newTestServer(t, drv)
	seedJob(t, store, "job-1", nil)

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/scans/job-1", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if _, err := store.LoadMeta("job-1"); err == nil {
		t.Error("expected job to be deleted")
	}
}

func TestHandleDeleteScan_NotFound(t *testing.T) {
	drv := &fakeDriver{info: driver.Info{Name: "doxie-dx400"}}
	srv, _ := newTestServer(t, drv)

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/scans/nope", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestHandleReorderPages(t *testing.T) {
	drv := &fakeDriver{info: driver.Info{Name: "doxie-dx400"}}
	srv, store := newTestServer(t, drv)
	seedJob(t, store, "job-1", []storage.PageMeta{
		{Index: 1, File: "page-001.png"},
		{Index: 2, File: "page-002.png"},
		{Index: 3, File: "page-003.png"},
	})

	rec := httptest.NewRecorder()
	body := bytes.NewBufferString(`{"order":[3,1,2]}`)
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/api/scans/job-1/pages/order", body))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}

	meta, err := store.LoadMeta("job-1")
	if err != nil {
		t.Fatalf("LoadMeta: %v", err)
	}
	got := []int{meta.Pages[0].Index, meta.Pages[1].Index, meta.Pages[2].Index}
	want := []int{3, 1, 2}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Pages order = %v, want %v", got, want)
		}
	}
}

func TestHandleReorderPages_WrongLength(t *testing.T) {
	drv := &fakeDriver{info: driver.Info{Name: "doxie-dx400"}}
	srv, store := newTestServer(t, drv)
	seedJob(t, store, "job-1", []storage.PageMeta{
		{Index: 1, File: "page-001.png"},
		{Index: 2, File: "page-002.png"},
	})

	rec := httptest.NewRecorder()
	body := bytes.NewBufferString(`{"order":[1]}`)
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/api/scans/job-1/pages/order", body))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestHandleReorderPages_NotAPermutation(t *testing.T) {
	drv := &fakeDriver{info: driver.Info{Name: "doxie-dx400"}}
	srv, store := newTestServer(t, drv)
	seedJob(t, store, "job-1", []storage.PageMeta{
		{Index: 1, File: "page-001.png"},
		{Index: 2, File: "page-002.png"},
	})

	rec := httptest.NewRecorder()
	body := bytes.NewBufferString(`{"order":[1,1]}`)
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/api/scans/job-1/pages/order", body))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestHandleReorderPages_UnknownIndex(t *testing.T) {
	drv := &fakeDriver{info: driver.Info{Name: "doxie-dx400"}}
	srv, store := newTestServer(t, drv)
	seedJob(t, store, "job-1", []storage.PageMeta{
		{Index: 1, File: "page-001.png"},
		{Index: 2, File: "page-002.png"},
	})

	rec := httptest.NewRecorder()
	body := bytes.NewBufferString(`{"order":[1,99]}`)
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/api/scans/job-1/pages/order", body))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestHandleReorderPages_BadJSON(t *testing.T) {
	drv := &fakeDriver{info: driver.Info{Name: "doxie-dx400"}}
	srv, store := newTestServer(t, drv)
	seedJob(t, store, "job-1", nil)

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/api/scans/job-1/pages/order", bytes.NewBufferString("not json")))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestHandleReorderPages_JobNotFound(t *testing.T) {
	drv := &fakeDriver{info: driver.Info{Name: "doxie-dx400"}}
	srv, _ := newTestServer(t, drv)

	rec := httptest.NewRecorder()
	body := bytes.NewBufferString(`{"order":[]}`)
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/api/scans/nope/pages/order", body))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestHandleGetPage(t *testing.T) {
	drv := &fakeDriver{info: driver.Info{Name: "doxie-dx400"}}
	srv, store := newTestServer(t, drv)
	seedJob(t, store, "job-1", []storage.PageMeta{
		{Index: 1, File: "page-001.png"},
		{Index: 2, File: "page-002.png"},
	})
	page1Bytes := testPNGBytes(t, 10, 10, color.NRGBA{R: 255, A: 255})
	page2Bytes := testPNGBytes(t, 10, 10, color.NRGBA{B: 255, A: 255})
	if err := store.SavePageFile("job-1", "page-001.png", page1Bytes); err != nil {
		t.Fatal(err)
	}
	if err := store.SavePageFile("job-1", "page-002.png", page2Bytes); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/scans/job-1/pages/1", nil))
	if rec.Code != http.StatusOK || !bytes.Equal(rec.Body.Bytes(), page1Bytes) {
		t.Fatalf("page 1 mismatch, status=%d", rec.Code)
	}

	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/scans/job-1/pages/2", nil))
	if rec.Code != http.StatusOK || !bytes.Equal(rec.Body.Bytes(), page2Bytes) {
		t.Fatalf("page 2 mismatch, status=%d", rec.Code)
	}
}

func TestHandleGetPage_InvalidPageNumber(t *testing.T) {
	drv := &fakeDriver{info: driver.Info{Name: "doxie-dx400"}}
	srv, store := newTestServer(t, drv)
	seedJob(t, store, "job-1", nil)

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/scans/job-1/pages/abc", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestHandleGetPage_JobNotFound(t *testing.T) {
	drv := &fakeDriver{info: driver.Info{Name: "doxie-dx400"}}
	srv, _ := newTestServer(t, drv)

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/scans/nope/pages/1", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestHandleGetPage_PageNotFound(t *testing.T) {
	drv := &fakeDriver{info: driver.Info{Name: "doxie-dx400"}}
	srv, store := newTestServer(t, drv)
	seedJob(t, store, "job-1", nil)

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/scans/job-1/pages/1", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestHandleDeletePage(t *testing.T) {
	drv := &fakeDriver{info: driver.Info{Name: "doxie-dx400"}}
	srv, store := newTestServer(t, drv)
	seedJob(t, store, "job-1", []storage.PageMeta{
		{Index: 1, File: "page-001.png"},
		{Index: 2, File: "page-002.png"},
	})
	page2Bytes := testPNGBytes(t, 5, 5, color.NRGBA{B: 255, A: 255})
	if err := store.SavePageFile("job-1", "page-001.png", testPNGBytes(t, 5, 5, color.NRGBA{R: 255, A: 255})); err != nil {
		t.Fatal(err)
	}
	if err := store.SavePageFile("job-1", "page-002.png", page2Bytes); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/scans/job-1/pages/1", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}

	// Deleting page 1 should renumber the former page 2 down to 1 — no
	// gap left behind.
	meta, err := store.LoadMeta("job-1")
	if err != nil {
		t.Fatalf("LoadMeta: %v", err)
	}
	if len(meta.Pages) != 1 || meta.Pages[0].Index != 1 || meta.Pages[0].File != "page-001.png" || meta.PageCount != 1 {
		t.Errorf("unexpected pages after delete: %+v", meta.Pages)
	}
	data, err := store.LoadPageFile("job-1", "page-001.png")
	if err != nil {
		t.Fatalf("LoadPageFile: %v", err)
	}
	if !bytes.Equal(data, page2Bytes) {
		t.Error("expected the renumbered page-001.png to hold the former page 2's content")
	}
	if _, err := store.LoadPageFile("job-1", "page-002.png"); err == nil {
		t.Error("expected the old page-002.png to no longer exist")
	}
}

func TestHandleDeletePage_RenumbersAroundAGapSafely(t *testing.T) {
	drv := &fakeDriver{info: driver.Info{Name: "doxie-dx400"}}
	srv, store := newTestServer(t, drv)
	seedJob(t, store, "job-1", []storage.PageMeta{
		{Index: 1, File: "page-001.png"},
		{Index: 2, File: "page-002.png"},
		{Index: 3, File: "page-003.png"},
		{Index: 4, File: "page-004.png"},
	})
	contents := map[string][]byte{
		"page-001.png": testPNGBytes(t, 5, 5, color.NRGBA{R: 255, A: 255}),
		"page-002.png": testPNGBytes(t, 5, 5, color.NRGBA{G: 255, A: 255}),
		"page-003.png": testPNGBytes(t, 5, 5, color.NRGBA{B: 255, A: 255}),
		"page-004.png": testPNGBytes(t, 5, 5, color.NRGBA{R: 255, G: 255, A: 255}),
	}
	for name, data := range contents {
		if err := store.SavePageFile("job-1", name, data); err != nil {
			t.Fatal(err)
		}
	}

	// Delete page 2: pages 3 and 4 must renumber to 2 and 3. A naive
	// same-name overwrite (renaming page-003.png straight to page-002.png)
	// would be fine here since nothing else targets page-002.png at that
	// moment, but this is exactly the scenario that would corrupt data
	// with an unsafe rename order after a drag-reorder shuffles the list.
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/scans/job-1/pages/2", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204, body=%s", rec.Code, rec.Body.String())
	}

	meta, err := store.LoadMeta("job-1")
	if err != nil {
		t.Fatalf("LoadMeta: %v", err)
	}
	if len(meta.Pages) != 3 {
		t.Fatalf("expected 3 pages, got %+v", meta.Pages)
	}
	wantIndexes := []int{1, 2, 3}
	wantContent := [][]byte{contents["page-001.png"], contents["page-003.png"], contents["page-004.png"]}
	for i, p := range meta.Pages {
		if p.Index != wantIndexes[i] {
			t.Errorf("page %d: index = %d, want %d", i, p.Index, wantIndexes[i])
		}
		data, err := store.LoadPageFile("job-1", p.File)
		if err != nil {
			t.Fatalf("LoadPageFile(%s): %v", p.File, err)
		}
		if !bytes.Equal(data, wantContent[i]) {
			t.Errorf("page %d (%s): content doesn't match the expected original image — renumbering likely clobbered a file", i, p.File)
		}
	}
}

func TestHandleDeletePage_RenumbersOutOfOrderPagesSafely(t *testing.T) {
	// After a drag-reorder, meta.Pages can be in a different order than
	// their File names' numeric order (e.g. display order [3,1,2] while
	// files are still page-003.png/page-001.png/page-002.png). Renumbering
	// must handle this without one rename clobbering another page's
	// not-yet-processed file.
	drv := &fakeDriver{info: driver.Info{Name: "doxie-dx400"}}
	srv, store := newTestServer(t, drv)
	seedJob(t, store, "job-1", []storage.PageMeta{
		{Index: 3, File: "page-003.png"},
		{Index: 1, File: "page-001.png"},
		{Index: 2, File: "page-002.png"},
		{Index: 4, File: "page-004.png"},
	})
	contents := map[string][]byte{
		"page-003.png": testPNGBytes(t, 5, 5, color.NRGBA{R: 255, A: 255}),
		"page-001.png": testPNGBytes(t, 5, 5, color.NRGBA{G: 255, A: 255}),
		"page-002.png": testPNGBytes(t, 5, 5, color.NRGBA{B: 255, A: 255}),
		"page-004.png": testPNGBytes(t, 5, 5, color.NRGBA{R: 255, G: 255, A: 255}),
	}
	for name, data := range contents {
		if err := store.SavePageFile("job-1", name, data); err != nil {
			t.Fatal(err)
		}
	}

	// Delete the last displayed page (original index 4); the first three
	// displayed pages (originally indexed 3,1,2) must renumber to 1,2,3
	// in that display order, without any rename clobbering another page.
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/scans/job-1/pages/4", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204, body=%s", rec.Code, rec.Body.String())
	}

	meta, err := store.LoadMeta("job-1")
	if err != nil {
		t.Fatalf("LoadMeta: %v", err)
	}
	if len(meta.Pages) != 3 {
		t.Fatalf("expected 3 pages, got %+v", meta.Pages)
	}
	wantContent := [][]byte{contents["page-003.png"], contents["page-001.png"], contents["page-002.png"]}
	for i, p := range meta.Pages {
		if p.Index != i+1 {
			t.Errorf("page %d: index = %d, want %d", i, p.Index, i+1)
		}
		data, err := store.LoadPageFile("job-1", p.File)
		if err != nil {
			t.Fatalf("LoadPageFile(%s): %v", p.File, err)
		}
		if !bytes.Equal(data, wantContent[i]) {
			t.Errorf("page %d (%s): content mismatch — renumbering likely clobbered a file", i, p.File)
		}
	}
}

func TestHandleDeletePage_NotFound(t *testing.T) {
	drv := &fakeDriver{info: driver.Info{Name: "doxie-dx400"}}
	srv, store := newTestServer(t, drv)
	seedJob(t, store, "job-1", nil)

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/scans/job-1/pages/1", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestHandleDeletePage_InvalidPageNumber(t *testing.T) {
	drv := &fakeDriver{info: driver.Info{Name: "doxie-dx400"}}
	srv, store := newTestServer(t, drv)
	seedJob(t, store, "job-1", nil)

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/scans/job-1/pages/0", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestHandleDeletePage_JobNotFound(t *testing.T) {
	drv := &fakeDriver{info: driver.Info{Name: "doxie-dx400"}}
	srv, _ := newTestServer(t, drv)

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/scans/nope/pages/1", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}
