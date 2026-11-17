package api

import (
	"archive/zip"
	"bytes"
	"image/color"
	"image/jpeg"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/andiapps-dev/doxie-scanner/internal/driver"
	"github.com/andiapps-dev/doxie-scanner/internal/storage"
)

func postImages(srv http.Handler, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/export/images", bytes.NewBufferString(body)))
	return rec
}

func zipEntries(t *testing.T, rec *httptest.ResponseRecorder) ([]string, [][]byte) {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
	if err != nil {
		t.Fatalf("not a zip: %v", err)
	}
	var names []string
	var datas [][]byte
	for _, f := range zr.File {
		rc, _ := f.Open()
		b, _ := io.ReadAll(rc)
		rc.Close()
		names = append(names, f.Name)
		datas = append(datas, b)
	}
	return names, datas
}

func TestHandleExportImages_PNGPassthroughInOrder(t *testing.T) {
	srv, store := newTestServer(t, &fakeDriver{info: driver.Info{Name: "doxie-dx400"}})
	seedPageJob(t, store, "job-1")
	seedPageJob(t, store, "job-2")
	want1, _ := store.LoadPageFile("job-1", "page-001.png")

	rec := postImages(srv, `{"pages":[{"jobId":"job-2","page":1},{"jobId":"job-1","page":1}],"title":"My: scan/1","format":"png"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/zip" {
		t.Errorf("Content-Type = %q", ct)
	}
	if cd := rec.Header().Get("Content-Disposition"); cd != `attachment; filename="My- scan-1.zip"` {
		t.Errorf("Content-Disposition = %q", cd)
	}
	names, datas := zipEntries(t, rec)
	if len(names) != 2 || names[0] != "My- scan-1-001.png" || names[1] != "My- scan-1-002.png" {
		t.Fatalf("names = %v", names)
	}
	if !bytes.Equal(datas[1], want1) {
		t.Error("PNG was not passed through byte-identical")
	}
}

func TestHandleExportImages_JPEG(t *testing.T) {
	srv, store := newTestServer(t, &fakeDriver{info: driver.Info{Name: "doxie-dx400"}})
	seedJob(t, store, "job-1", []storage.PageMeta{{Index: 1, File: "page-001.png"}})
	if err := store.SavePageFile("job-1", "page-001.png", testPNGBytes(t, 12, 8, color.NRGBA{G: 200, A: 255})); err != nil {
		t.Fatal(err)
	}
	rec := postImages(srv, `{"pages":[{"jobId":"job-1","page":1}],"format":"jpeg","quality":50}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	names, datas := zipEntries(t, rec)
	if names[0] != "pages-001.jpg" {
		t.Errorf("name = %q", names[0])
	}
	img, err := jpeg.Decode(bytes.NewReader(datas[0]))
	if err != nil || img.Bounds().Dx() != 12 {
		t.Fatalf("not a 12px JPEG: %v", err)
	}
}

func TestHandleExportImages_Validation(t *testing.T) {
	srv, store := newTestServer(t, &fakeDriver{info: driver.Info{Name: "doxie-dx400"}})
	seedPageJob(t, store, "job-1")
	cases := []struct {
		name, body string
		want       int
	}{
		{"bad json", `{`, 400},
		{"no pages", `{"pages":[],"format":"png"}`, 400},
		{"bad format", `{"pages":[{"jobId":"job-1","page":1}],"format":"gif"}`, 400},
		{"bad quality", `{"pages":[{"jobId":"job-1","page":1}],"format":"jpeg","quality":101}`, 400},
		{"unknown job", `{"pages":[{"jobId":"nope","page":1}],"format":"png"}`, 404},
		{"unknown page", `{"pages":[{"jobId":"job-1","page":9}],"format":"png"}`, 404},
		// the bad second ref must fail before any zip byte is written
		{"late bad ref", `{"pages":[{"jobId":"job-1","page":1},{"jobId":"job-1","page":9}],"format":"png"}`, 404},
	}
	for _, c := range cases {
		rec := postImages(srv, c.body)
		if rec.Code != c.want {
			t.Errorf("%s: status = %d, want %d", c.name, rec.Code, c.want)
		}
		if rec.Header().Get("Content-Type") == "application/zip" {
			t.Errorf("%s: started a zip", c.name)
		}
	}
}

func TestHandleExportImages_UnreadablePageAborts(t *testing.T) {
	srv, store := newTestServer(t, &fakeDriver{info: driver.Info{Name: "doxie-dx400"}})
	for _, format := range []string{"png", "jpeg"} {
		id := "job-" + format
		seedJob(t, store, id, []storage.PageMeta{{Index: 1, File: "page-001.png"}})
		if format == "jpeg" {
			_ = store.SavePageFile(id, "page-001.png", []byte("not a png"))
		}
		func() {
			defer func() {
				if r := recover(); r != http.ErrAbortHandler {
					t.Errorf("%s: recover = %v, want ErrAbortHandler", format, r)
				}
			}()
			postImages(srv, `{"pages":[{"jobId":"`+id+`","page":1}],"format":"`+format+`"}`)
		}()
	}
}

func TestSafeName(t *testing.T) {
	for in, want := range map[string]string{"": "x", "  ": "x", "...": "x", "a/b\\c": "a-b-c", " ok. ": "ok"} {
		if got := safeName(in, "x"); got != want {
			t.Errorf("safeName(%q) = %q, want %q", in, got, want)
		}
	}
}
