// Ported from github.com/jasonlearst/doxie-scanner (MIT), which forked this
// project; unchanged apart from this note.
// Package paperless is a small client for the Paperless-ngx REST API:
// enough to list tags, correspondents and document types, upload a PDF
// with metadata, and follow the consumption task it starts.
package paperless

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// apiVersion is the newest Paperless REST API version this client reads.
const apiVersion = 10

// Client talks to one Paperless-ngx instance with an API token.
type Client struct {
	baseURL string
	token   string
	http    *http.Client

	mu      sync.Mutex
	agreed  bool
	version int
}

// New returns a client for the instance at baseURL (e.g.
// "https://paperless.lan"), authenticating with token.
func New(baseURL, token string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		http:    &http.Client{Timeout: 2 * time.Minute},
	}
}

// BaseURL is the instance's address, for linking to documents.
func (c *Client) BaseURL() string { return c.baseURL }

// Item is a tag, correspondent or document type.
type Item struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// Metadata is everything the upload form can choose from.
type Metadata struct {
	Tags           []Item `json:"tags"`
	Correspondents []Item `json:"correspondents"`
	DocumentTypes  []Item `json:"documentTypes"`
}

// Document is what to upload and how to file it. Zero IDs are left for
// Paperless's own matching rules and workflows to decide.
type Document struct {
	PDF           []byte
	Filename      string
	Title         string
	Created       string
	Correspondent int
	DocumentType  int
	Tags          []int
}

// Task states, normalized from Paperless's Celery states.
const (
	TaskQueued     = "queued"
	TaskProcessing = "processing"
	TaskDone       = "done"
	TaskFailed     = "failed"
)

// Task is the progress of one upload's consumption.
type Task struct {
	Status     string
	DocumentID int
	Message    string
}

// ErrUnauthorized means the token was rejected.
var ErrUnauthorized = errors.New("paperless rejected the API token")

var errNotAcceptable = errors.New("paperless no longer serves the API version this app asked for")

func (c *Client) do(ctx context.Context, method, path string, body io.Reader, contentType string) (*http.Response, error) {
	version, err := c.agreeVersion(ctx)
	if err != nil {
		return nil, err
	}
	resp, err := c.send(ctx, method, path, body, contentType, version)
	if errors.Is(err, errNotAcceptable) {
		c.mu.Lock()
		c.agreed = false
		c.mu.Unlock()
	}
	return resp, err
}

// agreeVersion follows the Paperless docs: read X-Api-Version from one
// authenticated response, then pin the newest version both sides know
// in every request's Accept header. A server too old to send the header
// gets no version at all.
func (c *Client) agreeVersion(ctx context.Context) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.agreed {
		return c.version, nil
	}
	resp, err := c.send(ctx, http.MethodGet, "/api/tags/?page_size=1", nil, "", 0)
	if err != nil {
		return 0, err
	}
	resp.Body.Close()
	server, _ := strconv.Atoi(resp.Header.Get("X-Api-Version"))
	c.version = min(server, apiVersion)
	c.agreed = true
	return c.version, nil
}

func (c *Client) send(ctx context.Context, method, path string, body io.Reader, contentType string, version int) (*http.Response, error) {
	target := path
	if !strings.HasPrefix(path, "http") {
		target = c.baseURL + path
	}
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Token "+c.token)
	accept := "application/json"
	if version > 0 {
		accept += "; version=" + strconv.Itoa(version)
	}
	req.Header.Set("Accept", accept)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("paperless: %w", err)
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		resp.Body.Close()
		return nil, ErrUnauthorized
	}
	if resp.StatusCode >= 300 {
		defer resp.Body.Close()
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		if resp.StatusCode == http.StatusNotAcceptable {
			return nil, fmt.Errorf("%w (version %d): %s", errNotAcceptable, version, strings.TrimSpace(string(msg)))
		}
		return nil, fmt.Errorf("paperless: %s %s: %s %s", method, path, resp.Status, strings.TrimSpace(string(msg)))
	}
	return resp, nil
}

func (c *Client) getJSON(ctx context.Context, path string, v any) error {
	resp, err := c.do(ctx, http.MethodGet, path, nil, "")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		return fmt.Errorf("paperless: decode %s: %w", path, err)
	}
	return nil
}

// list follows every page of a list endpoint.
func (c *Client) list(ctx context.Context, path string) ([]Item, error) {
	all := []Item{} // an empty list, not null, so the app can always loop over it
	next := path + "?page_size=500&ordering=name"
	for next != "" {
		var page struct {
			Next    *string `json:"next"`
			Results []Item  `json:"results"`
		}
		if err := c.getJSON(ctx, next, &page); err != nil {
			return nil, err
		}
		all = append(all, page.Results...)
		next = ""
		if page.Next != nil {
			next = c.ownURL(*page.Next)
		}
	}
	return all, nil
}

// ownURL makes a "next page" link from a response safe to follow with the
// API token. Paperless builds those links from the request it received, so
// behind a proxy (or with a wrongly configured address) one can name a
// different host, and following it would hand the token to that host. A
// link to another origin is rewritten to the same path on the configured
// address; one already on it is returned as is.
func (c *Client) ownURL(next string) string {
	u, err := url.Parse(next)
	if err != nil || !u.IsAbs() {
		return next
	}
	base, err := url.Parse(c.baseURL)
	if err != nil || (u.Scheme == base.Scheme && u.Host == base.Host) {
		return next
	}
	// The configured address may itself sit under a path prefix; send()
	// puts that back, so take it off here to avoid doubling it.
	path := strings.TrimPrefix(u.Path, strings.TrimRight(base.Path, "/"))
	u.Path, u.RawPath = path, ""
	return u.RequestURI()
}

// Metadata fetches every tag, correspondent and document type.
func (c *Client) Metadata(ctx context.Context) (Metadata, error) {
	var m Metadata
	var err error
	if m.Tags, err = c.list(ctx, "/api/tags/"); err != nil {
		return m, err
	}
	if m.Correspondents, err = c.list(ctx, "/api/correspondents/"); err != nil {
		return m, err
	}
	if m.DocumentTypes, err = c.list(ctx, "/api/document_types/"); err != nil {
		return m, err
	}
	return m, nil
}

// Upload hands a PDF to Paperless's consumer and returns the task ID to
// follow with Task.
func (c *Client) Upload(ctx context.Context, doc Document) (string, error) {
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	file, err := form.CreateFormFile("document", doc.Filename)
	if err != nil {
		return "", err
	}
	if _, err := file.Write(doc.PDF); err != nil {
		return "", err
	}
	field := func(name, value string) {
		if value != "" {
			_ = form.WriteField(name, value)
		}
	}
	id := func(n int) string {
		if n == 0 {
			return ""
		}
		return strconv.Itoa(n)
	}
	field("title", doc.Title)
	field("created", doc.Created)
	field("correspondent", id(doc.Correspondent))
	field("document_type", id(doc.DocumentType))
	for _, t := range doc.Tags {
		field("tags", id(t))
	}
	if err := form.Close(); err != nil {
		return "", err
	}

	resp, err := c.do(ctx, http.MethodPost, "/api/documents/post_document/", &body, form.FormDataContentType())
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	var taskID string
	if json.Unmarshal(raw, &taskID) != nil {
		taskID = strings.Trim(strings.TrimSpace(string(raw)), `"`)
	}
	if taskID == "" {
		return "", errors.New("paperless: upload accepted but returned no task ID")
	}
	return taskID, nil
}

// taskRecord covers both task shapes: API v9 sends a bare list with
// uppercase states, result and related_document; v10 (Paperless 3)
// paginates, lowercases states and moves the outcome into result_data.
type taskRecord struct {
	Status          string          `json:"status"`
	Result          *string         `json:"result"`
	RelatedDocument json.RawMessage `json:"related_document"`
	ResultData      *struct {
		DocumentID   json.RawMessage `json:"document_id"`
		DuplicateOf  json.RawMessage `json:"duplicate_of"`
		Reason       string          `json:"reason"`
		ErrorMessage string          `json:"error_message"`
	} `json:"result_data"`
}

// Task reports how consumption of an upload is going.
func (c *Client) Task(ctx context.Context, taskID string) (Task, error) {
	path := "/api/tasks/?task_id=" + url.QueryEscape(taskID)
	var raw json.RawMessage
	if err := c.getJSON(ctx, path, &raw); err != nil {
		return Task{}, err
	}
	var tasks []taskRecord
	if err := json.Unmarshal(raw, &tasks); err != nil {
		var page struct {
			Results []taskRecord `json:"results"`
		}
		if json.Unmarshal(raw, &page) != nil {
			return Task{}, fmt.Errorf("paperless: decode %s: %w", path, err)
		}
		tasks = page.Results
	}
	if len(tasks) == 0 {
		return Task{Status: TaskQueued}, nil
	}
	t := tasks[0]
	out := Task{Message: t.message()}
	switch strings.ToUpper(t.Status) {
	case "SUCCESS":
		out.Status = TaskDone
		out.DocumentID = documentID(t.RelatedDocument)
		if out.DocumentID == 0 && t.ResultData != nil {
			out.DocumentID = documentID(t.ResultData.DocumentID)
		}
	case "FAILURE", "REVOKED":
		out.Status = TaskFailed
	case "STARTED":
		out.Status = TaskProcessing
	default:
		out.Status = TaskQueued
	}
	return out, nil
}

// message mirrors how Paperless itself turns v10 result_data back into
// the v9 result text, leaving out the success line nobody shows.
func (t taskRecord) message() string {
	switch {
	case t.Result != nil:
		return *t.Result
	case t.ResultData == nil:
		return ""
	case t.ResultData.Reason != "":
		return t.ResultData.Reason
	case documentID(t.ResultData.DuplicateOf) != 0:
		return fmt.Sprintf("It is a duplicate of document #%d", documentID(t.ResultData.DuplicateOf))
	default:
		return t.ResultData.ErrorMessage
	}
}

// documentID reads related_document, which Paperless has sent both as a
// number and as a string.
func documentID(raw json.RawMessage) int {
	var n int
	if json.Unmarshal(raw, &n) == nil {
		return n
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		n, _ = strconv.Atoi(s)
	}
	return n
}
