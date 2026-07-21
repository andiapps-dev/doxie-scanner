package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"testing"

	"github.com/andiapps-dev/doxie-scanner/internal/driver"
)

// requireOCRTools skips (rather than fails) when unpaper/tesseract
// aren't installed locally — CI always has both (see ci.yml). Mirrors
// internal/ocr's own test helper: handleExtractText calls the real
// ocr.ExtractText, the same way handleRotatePage/handleCropPage call the
// real doxiedx400.Rotate/Crop rather than a fake.
func requireOCRTools(t *testing.T) {
	t.Helper()
	for _, tool := range []string{"unpaper", "tesseract"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not installed locally; CI installs it (see ci.yml)", tool)
		}
	}
}

func TestHandleExtractText_Success(t *testing.T) {
	requireOCRTools(t)

	drv := &fakeDriver{info: driver.Info{Name: "doxie-dx400"}}
	srv, store := newTestServer(t, drv)
	seedPageJob(t, store, "job-1")

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/scans/job-1/pages/1/ocr", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if _, ok := body["text"]; !ok {
		t.Errorf("expected a \"text\" field in the response, got %v", body)
	}
}

func TestHandleExtractText_JobNotFound(t *testing.T) {
	requireOCRTools(t)

	drv := &fakeDriver{info: driver.Info{Name: "doxie-dx400"}}
	srv, _ := newTestServer(t, drv)

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/scans/nope/pages/1/ocr", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestHandleExtractText_PageNotFound(t *testing.T) {
	requireOCRTools(t)

	drv := &fakeDriver{info: driver.Info{Name: "doxie-dx400"}}
	srv, store := newTestServer(t, drv)
	seedPageJob(t, store, "job-1")

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/scans/job-1/pages/99/ocr", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestHandleExtractText_CorruptStoredFile(t *testing.T) {
	drv := &fakeDriver{info: driver.Info{Name: "doxie-dx400"}}
	srv, store := newTestServer(t, drv)
	seedPageJob(t, store, "job-1")
	if err := store.SavePageFile("job-1", "page-001.png", []byte("not a png")); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/scans/job-1/pages/1/ocr", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500, body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleExtractText_ToolNotAvailable(t *testing.T) {
	drv := &fakeDriver{info: driver.Info{Name: "doxie-dx400"}}
	srv, store := newTestServer(t, drv)
	seedPageJob(t, store, "job-1")

	t.Setenv("PATH", t.TempDir()) // neither unpaper nor tesseract resolve

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/scans/job-1/pages/1/ocr", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500, body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Error.Code != "ocr_unavailable" {
		t.Errorf("error code = %q, want %q", body.Error.Code, "ocr_unavailable")
	}
}
