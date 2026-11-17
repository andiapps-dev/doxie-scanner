package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/andiapps-dev/doxie-scanner/internal/driver"
	"github.com/andiapps-dev/doxie-scanner/internal/paperless"
	"github.com/andiapps-dev/doxie-scanner/internal/scanjobs"
	"github.com/andiapps-dev/doxie-scanner/internal/storage"
)

func stringsReader(s string) io.Reader { return strings.NewReader(s) }

// fakePaperlessServer serves just enough of Paperless-ngx for the send
// flow, and records what was uploaded.
type fakePaperlessServer struct {
	client *paperless.Client

	mu         sync.Mutex
	uploads    []map[string][]string // form fields of each upload
	pdfSizes   []int
	taskStatus string // "STARTED", "SUCCESS", "FAILURE", ...
	failUpload bool
	unauth     bool
	taskErr    map[string]bool // task ids whose lookup fails
}

func newFakePaperless(t *testing.T, _ *testing.T) *fakePaperlessServer {
	t.Helper()
	f := &fakePaperlessServer{taskStatus: "PENDING"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.unauth {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		list := func(items string) { fmt.Fprintf(w, `{"next":null,"results":[%s]}`, items) }
		switch {
		case r.URL.Path == "/api/tags/":
			list(`{"id":1,"name":"Insurance"}`)
		case r.URL.Path == "/api/correspondents/":
			list(`{"id":7,"name":"Cigna"}`)
		case r.URL.Path == "/api/document_types/":
			list(`{"id":3,"name":"Statement"}`)
		case r.URL.Path == "/api/documents/post_document/":
			if f.failUpload {
				http.Error(w, "boom", http.StatusInternalServerError)
				return
			}
			if err := r.ParseMultipartForm(32 << 20); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			f.uploads = append(f.uploads, r.MultipartForm.Value)
			if files := r.MultipartForm.File["document"]; len(files) == 1 {
				f.pdfSizes = append(f.pdfSizes, int(files[0].Size))
			}
			fmt.Fprintf(w, `"task-%d"`, len(f.uploads))
		case r.URL.Path == "/api/tasks/":
			if f.taskErr[r.URL.Query().Get("task_id")] {
				http.Error(w, "boom", http.StatusInternalServerError)
				return
			}
			fmt.Fprintf(w, `[{"status":%q,"related_document":"42","result":"failed because"}]`, f.taskStatus)
		default:
			fmt.Fprint(w, `{"count":0,"next":null,"results":[]}`)
		}
	}))
	t.Cleanup(srv.Close)
	f.client = paperless.New(srv.URL, "tok")
	return f
}

func paperlessServer(t *testing.T) (*Server, *storage.Store, *fakePaperlessServer) {
	t.Helper()
	srv, store := newTestServer(t, &fakeDriver{info: driver.Info{Name: "d"}})
	fake := newFakePaperless(t, t)
	srv.paperless = fake.client
	seedTwoPageJob(t, store, "job-a")
	seedTwoPageJob(t, store, "job-b")
	return srv, store, fake
}

func post(srv *Server, path, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)))
	return rec
}

// runningManager returns a manager with a scan that never finishes.
func runningManager(t *testing.T) *scanjobs.Manager {
	t.Helper()
	drv := &fakeDriver{info: driver.Info{Name: "d"}, session: &fakeSession{block: make(chan struct{})}}
	m := scanjobs.NewManager(drv, storage.New(t.TempDir()))
	if _, err := m.StartScan(false); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestPaperlessHandlers_NotConfigured(t *testing.T) {
	srv, _ := newTestServer(t, &fakeDriver{info: driver.Info{Name: "d"}})
	for name, rec := range map[string]*httptest.ResponseRecorder{
		"metadata": httptest.NewRecorder(),
	} {
		srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/paperless/metadata", nil))
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s: status = %d, want 503", name, rec.Code)
		}
	}
	if rec := post(srv, "/api/paperless/send", `{}`); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("send: status = %d", rec.Code)
	}
	if rec := post(srv, "/api/scans/x/sent/refresh", ``); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("refresh: status = %d", rec.Code)
	}
}

func TestPaperlessMetadata(t *testing.T) {
	srv, _, _ := paperlessServer(t)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/paperless/metadata", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var m paperless.Metadata
	_ = json.Unmarshal(rec.Body.Bytes(), &m)
	if len(m.Tags) != 1 || m.Tags[0].Name != "Insurance" || m.Correspondents[0].ID != 7 || m.DocumentTypes[0].Name != "Statement" {
		t.Errorf("metadata = %+v", m)
	}
}

func TestPaperlessMetadata_ErrorsMapToBadGateway(t *testing.T) {
	srv, _, fake := paperlessServer(t)
	fake.unauth = true
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/paperless/metadata", nil))
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "paperless_unauthorized") {
		t.Errorf("bad token: %d %s", rec.Code, rec.Body.String())
	}
	srv.paperless = paperless.New("http://127.0.0.1:1", "x")
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/paperless/metadata", nil))
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "paperless_unreachable") {
		t.Errorf("unreachable: %d %s", rec.Code, rec.Body.String())
	}
}

func TestSend_SelectedPagesFromTwoScansBecomeOneDocument(t *testing.T) {
	srv, store, fake := paperlessServer(t)
	body := `{"pages":[{"jobId":"job-b","page":2},{"jobId":"job-a","page":1},{"jobId":"job-b","page":1}],
		"title":"  Statement 2026  ","created":"2026-11-17","correspondent":7,"documentType":3,"tags":[1]}`
	rec := post(srv, "/api/paperless/send", body)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}

	if len(fake.uploads) != 1 {
		t.Fatalf("uploads = %d, want exactly one document", len(fake.uploads))
	}
	f := fake.uploads[0]
	if f["title"][0] != "Statement 2026" || f["created"][0] != "2026-11-17" || f["correspondent"][0] != "7" || f["document_type"][0] != "3" || f["tags"][0] != "1" {
		t.Errorf("form = %v", f)
	}
	if fake.pdfSizes[0] < 1000 {
		t.Errorf("pdf is %d bytes", fake.pdfSizes[0])
	}

	a, _ := store.LoadMeta("job-a")
	b, _ := store.LoadMeta("job-b")
	if len(a.Sent) != 1 || fmt.Sprint(a.Sent[0].Pages) != "[1]" || a.Sent[0].TaskID != "task-1" || a.Sent[0].Status != storage.SentQueued {
		t.Errorf("job-a sent = %+v", a.Sent)
	}
	if len(b.Sent) != 1 || fmt.Sprint(b.Sent[0].Pages) != "[2 1]" || b.Sent[0].Title != "Statement 2026" {
		t.Errorf("job-b sent = %+v, want its pages in selection order", b.Sent)
	}
}

func TestSend_DefaultsTitleAndLeavesOptionsToPaperless(t *testing.T) {
	srv, _, fake := paperlessServer(t)
	if rec := post(srv, "/api/paperless/send", `{"pages":[{"jobId":"job-a","page":1}]}`); rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	f := fake.uploads[0]
	if f["title"][0] != "Scan job-a" {
		t.Errorf("title = %q, want the scan's name", f["title"][0])
	}
	for _, k := range []string{"created", "correspondent", "document_type", "tags"} {
		if _, ok := f[k]; ok {
			t.Errorf("%s was sent although left empty", k)
		}
	}
}

func TestSend_Rejections(t *testing.T) {
	srv, store, fake := paperlessServer(t)
	for name, tc := range map[string]struct {
		body string
		want int
	}{
		"not json":        {`nope`, http.StatusBadRequest},
		"no pages":        {`{"pages":[]}`, http.StatusBadRequest},
		"bad date":        {`{"pages":[{"jobId":"job-a","page":1}],"created":"17/11/2026"}`, http.StatusBadRequest},
		"unknown scan":    {`{"pages":[{"jobId":"nope","page":1}]}`, http.StatusNotFound},
		"unknown page":    {`{"pages":[{"jobId":"job-a","page":9}]}`, http.StatusNotFound},
		"upload fails":    {`{"pages":[{"jobId":"job-a","page":1}]}`, http.StatusBadGateway},
		"page file gone":  {`{"pages":[{"jobId":"job-a","page":2}]}`, http.StatusInternalServerError},
		"page not a png":  {`{"pages":[{"jobId":"job-b","page":2}]}`, http.StatusInternalServerError},
		"scan is running": {`{"pages":[{"jobId":"job-a","page":1}]}`, http.StatusConflict},
	} {
		switch name {
		case "upload fails":
			fake.failUpload = true
		case "page file gone":
			_ = store.DeletePageFile("job-a", "page-002.png")
		case "page not a png":
			_ = store.SavePageFile("job-b", "page-002.png", []byte("junk"))
		case "scan is running":
			fake.failUpload = false
			srv.jobs = runningManager(t)
			meta := seedJob(t, store, srv.jobs.CurrentJob().ID, nil)
			tc.body = fmt.Sprintf(`{"pages":[{"jobId":%q,"page":1}]}`, meta.ID)
		}
		if rec := post(srv, "/api/paperless/send", tc.body); rec.Code != tc.want {
			t.Errorf("%s: status = %d, want %d (%s)", name, rec.Code, tc.want, rec.Body.String())
		}
	}
	if a, _ := store.LoadMeta("job-a"); len(a.Sent) != 0 {
		t.Errorf("a failed send left a record: %+v", a.Sent)
	}
}

func TestRefreshSent_FollowsTheTask(t *testing.T) {
	srv, store, fake := paperlessServer(t)
	post(srv, "/api/paperless/send", `{"pages":[{"jobId":"job-a","page":1}]}`)

	state := func() storage.SentDocument {
		rec := post(srv, "/api/scans/job-a/sent/refresh", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("refresh status = %d, body=%s", rec.Code, rec.Body.String())
		}
		var meta storage.JobMeta
		_ = json.Unmarshal(rec.Body.Bytes(), &meta)
		return meta.Sent[0]
	}

	fake.taskStatus = "STARTED"
	if d := state(); d.Status != storage.SentProcessing {
		t.Errorf("started: %+v", d)
	}
	fake.taskStatus = "SUCCESS"
	if d := state(); d.Status != storage.SentDone || d.DocumentID != 42 {
		t.Errorf("success: %+v", d)
	}
	saved, _ := store.LoadMeta("job-a")
	if saved.Sent[0].Status != storage.SentDone {
		t.Error("the new state was not saved")
	}
	// A finished upload is not asked about again.
	fake.unauth = true
	if d := state(); d.Status != storage.SentDone {
		t.Errorf("a done upload was re-checked: %+v", d)
	}
}

func TestRefreshSent_FailureCarriesPaperlessMessage(t *testing.T) {
	srv, _, fake := paperlessServer(t)
	post(srv, "/api/paperless/send", `{"pages":[{"jobId":"job-a","page":1}]}`)
	fake.taskStatus = "FAILURE"
	rec := post(srv, "/api/scans/job-a/sent/refresh", "")
	var meta storage.JobMeta
	_ = json.Unmarshal(rec.Body.Bytes(), &meta)
	if d := meta.Sent[0]; d.Status != storage.SentFailed || d.Error != "failed because" {
		t.Errorf("failure: %+v", d)
	}
}

func TestRefreshSent_Errors(t *testing.T) {
	srv, _, fake := paperlessServer(t)
	if rec := post(srv, "/api/scans/nope/sent/refresh", ""); rec.Code != http.StatusNotFound {
		t.Errorf("unknown scan: %d", rec.Code)
	}
	post(srv, "/api/paperless/send", `{"pages":[{"jobId":"job-a","page":1}]}`)
	fake.unauth = true
	if rec := post(srv, "/api/scans/job-a/sent/refresh", ""); rec.Code != http.StatusBadGateway {
		t.Errorf("paperless down: %d", rec.Code)
	}
}

func TestPdfFilename(t *testing.T) {
	for in, want := range map[string]string{
		"Statement 2026": "Statement 2026.pdf",
		"a/b\\c:d":       "a_b_c_d.pdf",
		"   ":            "scan.pdf",
		"":               "scan.pdf",
		"naïve":          "na_ve.pdf",
	} {
		if got := pdfFilename(in); got != want {
			t.Errorf("pdfFilename(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRefreshSent_OneFailedLookupDoesNotDiscardTheOthers(t *testing.T) {
	srv, store, fake := paperlessServer(t)
	post(srv, "/api/paperless/send", `{"pages":[{"jobId":"job-a","page":1}],"title":"first"}`)  // task-1
	post(srv, "/api/paperless/send", `{"pages":[{"jobId":"job-a","page":2}],"title":"second"}`) // task-2
	fake.taskStatus = "SUCCESS"
	fake.taskErr = map[string]bool{"task-1": true} // the first lookup fails, the second works

	rec := post(srv, "/api/scans/job-a/sent/refresh", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: part of the answer is still good (%s)", rec.Code, rec.Body.String())
	}
	saved, _ := store.LoadMeta("job-a")
	if len(saved.Sent) != 2 || saved.Sent[0].Status != storage.SentQueued || saved.Sent[1].Status != storage.SentDone {
		t.Errorf("sent = %+v, want the first still queued and the second done", saved.Sent)
	}

	// With every lookup failing and nothing learned, it says so.
	fake.taskErr = map[string]bool{"task-1": true, "task-2": true}
	if rec := post(srv, "/api/scans/job-a/sent/refresh", ""); rec.Code != http.StatusBadGateway {
		t.Errorf("all lookups failed: status = %d, want 502", rec.Code)
	}
}

func TestWithPaperlessOptionTurnsSendingOn(t *testing.T) {
	store := storage.New(t.TempDir())
	drv := &fakeDriver{info: driver.Info{Name: "d"}}
	fake := newFakePaperless(t, t)
	srv := NewServer(drv, scanjobs.NewManager(drv, store), store, nil, "test", "eng", WithPaperless(fake.client))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/paperless/metadata", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 with Paperless configured", rec.Code)
	}
}
