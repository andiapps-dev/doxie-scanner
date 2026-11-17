package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andiapps-dev/doxie-scanner/internal/driver"
	"github.com/andiapps-dev/doxie-scanner/internal/storage"
)

func bulkDelete(srv *Server, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/scans/bulk-delete", strings.NewReader(body)))
	return rec
}

func decodeBulk(t *testing.T, rec *httptest.ResponseRecorder) bulkDeleteResponse {
	t.Helper()
	var r bulkDeleteResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &r); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body.String())
	}
	return r
}

func TestBulkDelete_DeletesEveryScanInTheList(t *testing.T) {
	srv, store := newTestServer(t, &fakeDriver{info: driver.Info{Name: "d"}})
	for _, id := range []string{"a", "b", "c"} {
		seedJob(t, store, id, []storage.PageMeta{{Index: 1, File: "page-001.png"}})
		_ = store.SavePageFile(id, "page-001.png", []byte("x"))
	}
	rec := bulkDelete(srv, `{"ids":["a","c"]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	r := decodeBulk(t, rec)
	if len(r.Deleted) != 2 || r.Deleted[0] != "a" || r.Deleted[1] != "c" || len(r.Skipped) != 0 {
		t.Errorf("response = %+v", r)
	}
	if _, err := store.LoadMeta("a"); err == nil {
		t.Error("scan a is still there")
	}
	if _, err := store.LoadMeta("b"); err != nil {
		t.Error("scan b, which wasn't selected, was deleted")
	}
}

func TestBulkDelete_ReportsWhatItSkippedAndKeepsGoing(t *testing.T) {
	// A scan still running, one that's already gone, and a repeated id sit
	// in the middle of a list: the others are deleted all the same.
	drv := &fakeDriver{info: driver.Info{Name: "d"}, session: &fakeSession{block: make(chan struct{})}}
	srv, store := newTestServer(t, drv)
	running, err := srv.jobs.StartScan(false)
	if err != nil {
		t.Fatal(err)
	}
	seedJob(t, store, "done-1", nil)
	seedJob(t, store, "done-2", nil)

	rec := bulkDelete(srv, `{"ids":["done-1","`+running+`","ghost","done-1","done-2"]}`)
	r := decodeBulk(t, rec)
	if len(r.Deleted) != 2 || r.Deleted[0] != "done-1" || r.Deleted[1] != "done-2" {
		t.Errorf("deleted = %v", r.Deleted)
	}
	reasons := map[string]string{}
	for _, s := range r.Skipped {
		reasons[s.ID] = s.Reason
	}
	if len(r.Skipped) != 2 || reasons[running] != "scan_running" || reasons["ghost"] != "not_found" {
		t.Errorf("skipped = %+v (a repeated id must not be reported twice)", r.Skipped)
	}
	if _, err := store.LoadMeta(running); err != nil {
		t.Error("the running scan was deleted")
	}
}

func TestBulkDelete_Rejections(t *testing.T) {
	srv, _ := newTestServer(t, &fakeDriver{info: driver.Info{Name: "d"}})
	for name, body := range map[string]string{"not json": `nope`, "empty list": `{"ids":[]}`, "no ids": `{}`} {
		if rec := bulkDelete(srv, body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", name, rec.Code)
		}
	}
}

func TestBulkDelete_ReportsScansThatFailOnDisk(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("file permissions don't bind root")
	}
	srv, store := newTestServer(t, &fakeDriver{info: driver.Info{Name: "d"}})
	seedJob(t, store, "a", nil)
	seedJob(t, store, "b", nil)
	// With the jobs folder read-only, removing a scan's folder fails.
	jobs := filepath.Join(store.Root(), "jobs")
	if err := os.Chmod(jobs, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(jobs, 0o700)

	rec := bulkDelete(srv, `{"ids":["a"]}`)
	r := decodeBulk(t, rec)
	if rec.Code != http.StatusOK || len(r.Deleted) != 0 || len(r.Skipped) != 1 || r.Skipped[0].Reason != "internal_error" {
		t.Errorf("status %d, response = %+v", rec.Code, r)
	}
	// The single delete reports the same failure as a server error.
	one := httptest.NewRecorder()
	srv.ServeHTTP(one, httptest.NewRequest(http.MethodDelete, "/api/scans/b", nil))
	if one.Code != http.StatusInternalServerError {
		t.Errorf("single delete: status = %d, want 500", one.Code)
	}
}

func TestDeleteScan_RefusesARunningScan(t *testing.T) {
	drv := &fakeDriver{info: driver.Info{Name: "d"}, session: &fakeSession{block: make(chan struct{})}}
	srv, store := newTestServer(t, drv)
	id, _ := srv.jobs.StartScan(false)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/scans/"+id, nil))
	if rec.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409", rec.Code)
	}
	if _, err := store.LoadMeta(id); err != nil {
		t.Error("the running scan was deleted")
	}
}

func TestUnsentPages(t *testing.T) {
	job := storage.JobMeta{Pages: []storage.PageMeta{{Index: 1}, {Index: 2}, {Index: 3}, {Index: 4}}}
	if got := unsentPages(job); got != 4 {
		t.Errorf("nothing sent: %d, want 4", got)
	}
	job.Sent = []storage.SentDocument{
		{Pages: []int{1, 2}, Status: storage.SentDone},
		{Pages: []int{3}, Status: storage.SentFailed}, // a failed upload doesn't count
	}
	if got := unsentPages(job); got != 2 {
		t.Errorf("pages 1-2 sent, 3 failed: %d, want 2 (pages 3 and 4)", got)
	}
	if s := toSummary(job); s.UnsentPages != 2 {
		t.Errorf("summary says %d unsent", s.UnsentPages)
	}
}

func TestBulkDelete_CannotReachOutsideTheScansFolder(t *testing.T) {
	srv, store := newTestServer(t, &fakeDriver{info: driver.Info{Name: "d"}})
	seedJob(t, store, "real", nil)
	// A folder beside jobs/ that a path-joined id would reach, holding a
	// meta.json so it looks like a scan.
	victim := filepath.Join(store.Root(), "victim")
	_ = os.MkdirAll(victim, 0o755)
	_ = os.WriteFile(filepath.Join(victim, "meta.json"), []byte(`{"id":"victim"}`), 0o644)

	r := decodeBulk(t, bulkDelete(srv, `{"ids":["../victim","real/..","..","a/b","","real"]}`))
	if len(r.Deleted) != 1 || r.Deleted[0] != "real" {
		t.Errorf("deleted = %v, want only the real scan", r.Deleted)
	}
	if len(r.Skipped) != 5 {
		t.Errorf("skipped = %+v, want the five bad ids", r.Skipped)
	}
	for _, s := range r.Skipped {
		if s.Reason != "not_found" {
			t.Errorf("%q skipped as %q, want not_found", s.ID, s.Reason)
		}
	}
	if _, err := os.Stat(filepath.Join(victim, "meta.json")); err != nil {
		t.Errorf("a folder outside jobs/ was deleted: %v", err)
	}
}
