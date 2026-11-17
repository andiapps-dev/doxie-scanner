package paperless

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakePaperless serves just enough of the Paperless-ngx API, answering
// the client's one-off version check itself.
func fakePaperless(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Token secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if isVersionCheck(r) {
			fmt.Fprint(w, `{"count":0,"next":null,"results":[]}`)
			return
		}
		handler(w, r)
	}))
	t.Cleanup(srv.Close)
	return New(srv.URL+"/", "secret")
}

func TestMetadata_ReportsABadTokenAndAnUnreachableServer(t *testing.T) {
	c := fakePaperless(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"count":0,"next":null,"results":[]}`)
	})
	if _, err := c.Metadata(context.Background()); err != nil {
		t.Fatal(err)
	}
	bad := New(c.BaseURL(), "wrong")
	if _, err := bad.Metadata(context.Background()); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("a bad token: got %v", err)
	}
	if _, err := New("http://127.0.0.1:1", "x").Metadata(context.Background()); err == nil {
		t.Error("an unreachable server should fail")
	}
}

func TestMetadataFollowsPages(t *testing.T) {
	var base string
	c := fakePaperless(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/tags/" && r.URL.Query().Get("page") == "":
			fmt.Fprintf(w, `{"next":%q,"results":[{"id":1,"name":"Bills"}]}`, base+"/api/tags/?page=2")
		case r.URL.Path == "/api/tags/":
			fmt.Fprint(w, `{"next":null,"results":[{"id":2,"name":"Taxes"}]}`)
		case r.URL.Path == "/api/correspondents/":
			fmt.Fprint(w, `{"next":null,"results":[{"id":7,"name":"Bank"}]}`)
		case r.URL.Path == "/api/document_types/":
			fmt.Fprint(w, `{"next":null,"results":[{"id":3,"name":"Invoice"}]}`)
		}
	})
	base = c.BaseURL()

	m, err := c.Metadata(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Tags) != 2 || m.Tags[1].Name != "Taxes" || m.Correspondents[0].ID != 7 || m.DocumentTypes[0].Name != "Invoice" {
		t.Errorf("got %+v", m)
	}
}

// A Paperless with no correspondents or document types yet must still
// give the send sheet lists to show.
func TestMetadataEmptyListsAreEmptyNotNull(t *testing.T) {
	c := fakePaperless(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"next":null,"results":[]}`)
	})
	m, err := c.Metadata(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(m)
	if want := `{"tags":[],"correspondents":[],"documentTypes":[]}`; string(b) != want {
		t.Errorf("got %s, want %s", b, want)
	}
}

func TestMetadataErrors(t *testing.T) {
	for _, failing := range []string{"/api/tags/", "/api/correspondents/", "/api/document_types/"} {
		c := fakePaperless(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == failing {
				http.Error(w, "boom", http.StatusInternalServerError)
				return
			}
			fmt.Fprint(w, `{"next":null,"results":[]}`)
		})
		if _, err := c.Metadata(context.Background()); err == nil {
			t.Errorf("%s failing should fail Metadata", failing)
		}
	}
	c := fakePaperless(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "not json") })
	if _, err := c.Metadata(context.Background()); err == nil {
		t.Error("garbage should fail to decode")
	}
}

func TestUpload(t *testing.T) {
	c := fakePaperless(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/documents/post_document/" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatal(err)
		}
		f, hdr, err := r.FormFile("document")
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(f)
		got := fmt.Sprintf("%s|%s|%s|%s|%s|%v|%v", hdr.Filename, body, r.FormValue("title"), r.FormValue("correspondent"),
			r.FormValue("document_type"), r.MultipartForm.Value["tags"], r.MultipartForm.Value["created"])
		if got != "scan.pdf|%PDF|Water bill|7||[1 2]|[]" {
			t.Errorf("form: %s", got)
		}
		fmt.Fprint(w, `"4b1f-uuid"`)
	})
	id, err := c.Upload(context.Background(), Document{PDF: []byte("%PDF"), Filename: "scan.pdf", Title: "Water bill", Correspondent: 7, Tags: []int{1, 2}})
	if err != nil || id != "4b1f-uuid" {
		t.Errorf("got %q, %v", id, err)
	}
}

func TestUpload_ResponseShapes(t *testing.T) {
	for body, want := range map[string]string{`"abc"`: "abc", "abc\n": "abc", `""`: ""} {
		c := fakePaperless(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) })
		id, err := c.Upload(context.Background(), Document{PDF: []byte("x"), Filename: "a.pdf"})
		if want == "" {
			if err == nil {
				t.Errorf("%q: an empty task ID should fail", body)
			}
			continue
		}
		if err != nil || id != want {
			t.Errorf("%q: got %q, %v", body, id, err)
		}
	}
	c := fakePaperless(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "too big", http.StatusRequestEntityTooLarge)
	})
	if _, err := c.Upload(context.Background(), Document{PDF: []byte("x"), Filename: "a.pdf"}); err == nil || !strings.Contains(err.Error(), "too big") {
		t.Errorf("server errors should surface with their message, got %v", err)
	}
}

func TestTask(t *testing.T) {
	cases := map[string]Task{
		`[]`:                                   {Status: TaskQueued},
		`[{"status":"PENDING","result":null}]`: {Status: TaskQueued},
		`[{"status":"STARTED"}]`:               {Status: TaskProcessing},
		`[{"status":"SUCCESS","result":"Success","related_document":"42"}]`:           {Status: TaskDone, DocumentID: 42, Message: "Success"},
		`[{"status":"SUCCESS","related_document":43}]`:                                {Status: TaskDone, DocumentID: 43},
		`[{"status":"FAILURE","result":"Not consuming scan.pdf: It is a duplicate"}]`: {Status: TaskFailed, Message: "Not consuming scan.pdf: It is a duplicate"},

		`{"count":0,"next":null,"results":[]}`:                                                                {Status: TaskQueued},
		`{"results":[{"status":"pending","result_data":null}]}`:                                               {Status: TaskQueued},
		`{"results":[{"status":"started"}]}`:                                                                  {Status: TaskProcessing},
		`{"results":[{"status":"success","result_data":{"document_id":1158},"related_document_ids":[1158]}]}`: {Status: TaskDone, DocumentID: 1158},
		`{"results":[{"status":"failure","result_data":{"duplicate_of":12}}]}`:                                {Status: TaskFailed, Message: "It is a duplicate of document #12"},
		`{"results":[{"status":"failure","result_data":{"reason":"Unsupported type"}}]}`:                      {Status: TaskFailed, Message: "Unsupported type"},
		`{"results":[{"status":"failure","result_data":{"error_message":"OCR failed"}}]}`:                     {Status: TaskFailed, Message: "OCR failed"},
		`{"results":[{"status":"revoked"}]}`:                                                                  {Status: TaskFailed},
	}
	for body, want := range cases {
		c := fakePaperless(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("task_id") != "abc" {
				t.Errorf("task_id %q", r.URL.Query().Get("task_id"))
			}
			fmt.Fprint(w, body)
		})
		got, err := c.Task(context.Background(), "abc")
		if err != nil || got != want {
			t.Errorf("%s: got %+v, %v", body, got, err)
		}
	}
	c := fakePaperless(t, func(w http.ResponseWriter, r *http.Request) { http.Error(w, "down", http.StatusBadGateway) })
	if _, err := c.Task(context.Background(), "abc"); err == nil {
		t.Error("expected an error")
	}
	for _, body := range []string{"not json", `"a string"`} {
		c := fakePaperless(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) })
		if _, err := c.Task(context.Background(), "abc"); err == nil {
			t.Errorf("%s: expected a decode error", body)
		}
	}
}

func TestBadBaseURL(t *testing.T) {
	if _, err := New("http://bad host", "t").Metadata(context.Background()); err == nil {
		t.Error("an invalid URL should fail")
	}
}

func isVersionCheck(r *http.Request) bool {
	return r.URL.Path == "/api/tags/" && r.URL.Query().Get("page_size") == "1"
}

func TestAPIVersionIsPinnedFromTheServer(t *testing.T) {
	for server, want := range map[string]string{
		"":   "application/json",
		"9":  "application/json; version=9",
		"10": "application/json; version=10",
		"11": "application/json; version=10",
	} {
		var mu sync.Mutex
		checks, accept := 0, ""
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			if server != "" {
				w.Header().Set("X-Api-Version", server)
			}
			if isVersionCheck(r) {
				checks++
				fmt.Fprint(w, `{"results":[]}`)
				return
			}
			accept = r.Header.Get("Accept")
			fmt.Fprint(w, `[]`)
		}))
		c := New(srv.URL, "t")
		for range 2 {
			if _, err := c.Task(context.Background(), "abc"); err != nil {
				t.Fatal(err)
			}
		}
		if checks != 1 || accept != want {
			t.Errorf("server %q: %d version checks, Accept %q; want 1 and %q", server, checks, accept, want)
		}
		srv.Close()
	}
}

func TestAPIVersionIsCheckedAgainAfterARefusal(t *testing.T) {
	server, checks, accept := "10", 0, ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Api-Version", server)
		if isVersionCheck(r) {
			checks++
			fmt.Fprint(w, `{"results":[]}`)
			return
		}
		accept = r.Header.Get("Accept")
		if accept == "application/json; version=10" {
			w.WriteHeader(http.StatusNotAcceptable)
			fmt.Fprint(w, `{"detail":"Invalid version in \"Accept\" header."}`)
			return
		}
		fmt.Fprint(w, `[]`)
	}))
	defer srv.Close()
	c := New(srv.URL, "t")
	if _, err := c.Task(context.Background(), "abc"); !errors.Is(err, errNotAcceptable) || !strings.Contains(err.Error(), "Invalid version") {
		t.Fatalf("a refused version should say so, got %v", err)
	}
	server = "9"
	if _, err := c.Task(context.Background(), "abc"); err != nil {
		t.Fatal(err)
	}
	if checks != 2 || accept != "application/json; version=9" {
		t.Errorf("%d version checks, Accept %q", checks, accept)
	}
}

func TestOwnURL(t *testing.T) {
	c := New("https://paperless.lan/", "t")
	for in, want := range map[string]string{
		"https://paperless.lan/api/tags/?page=2": "https://paperless.lan/api/tags/?page=2", // ours: unchanged
		"/api/tags/?page=2":                      "/api/tags/?page=2",                      // relative: unchanged
		"http://internal:8000/api/tags/?page=2":  "/api/tags/?page=2",                      // another host: path only
		"https://evil.example/api/tags/?a=b&c=d": "/api/tags/?a=b&c=d",
		"%zz":                                    "%zz", // unparseable: left for the request to fail on
	} {
		if got := c.ownURL(in); got != want {
			t.Errorf("ownURL(%q) = %q, want %q", in, got, want)
		}
	}
	sub := New("https://host.lan/paperless/", "t")
	if got := sub.ownURL("http://internal/paperless/api/tags/?page=2"); got != "/api/tags/?page=2" {
		t.Errorf("under a path prefix: %q, want the prefix not doubled", got)
	}
}

func TestMetadata_NeverSendsTheTokenToAnotherHost(t *testing.T) {
	var strangerHits int
	stranger := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		strangerHits++
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(stranger.Close)

	c := fakePaperless(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/tags/" && r.URL.Query().Get("page") == "":
			// the first page points the next one at some other host
			fmt.Fprintf(w, `{"next":%q,"results":[{"id":1,"name":"Bills"}]}`, stranger.URL+"/api/tags/?page=2")
		case r.URL.Path == "/api/tags/":
			fmt.Fprint(w, `{"next":null,"results":[{"id":2,"name":"Taxes"}]}`)
		default:
			fmt.Fprint(w, `{"next":null,"results":[]}`)
		}
	})
	m, err := c.Metadata(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Tags) != 2 || m.Tags[1].Name != "Taxes" {
		t.Errorf("tags = %+v: the second page should still be read, from the configured server", m.Tags)
	}
	if strangerHits != 0 {
		t.Errorf("the other host was contacted %d time(s), so it would have received the API token", strangerHits)
	}
}
