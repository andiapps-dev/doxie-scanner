package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/andiapps-dev/doxie-scanner/internal/paperless"
	"github.com/andiapps-dev/doxie-scanner/internal/pdfexport"
	"github.com/andiapps-dev/doxie-scanner/internal/storage"
)

// Sending reuses the Paperless client from the jasonlearst/doxie-scanner
// fork (internal/paperless). What is sent is whatever pages the person has
// selected, from any scans, in the order they were put in the selection:
// one selection becomes one document.

const (
	uploadTimeout  = 3 * time.Minute
	refreshTimeout = 10 * time.Second
)

// requirePaperless writes a 503 when sending isn't set up.
func (s *Server) requirePaperless(w http.ResponseWriter) bool {
	if s.paperless == nil {
		writeError(w, http.StatusServiceUnavailable, "paperless_not_configured",
			"Paperless isn't set up: set DOXIE_PAPERLESS_URL and DOXIE_PAPERLESS_TOKEN.")
		return false
	}
	return true
}

func paperlessError(w http.ResponseWriter, err error) {
	if errors.Is(err, paperless.ErrUnauthorized) {
		writeError(w, http.StatusBadGateway, "paperless_unauthorized", "Paperless rejected the API token.")
		return
	}
	writeError(w, http.StatusBadGateway, "paperless_unreachable", err.Error())
}

// handlePaperlessMetadata lists the tags, correspondents and document
// types the send form chooses from.
func (s *Server) handlePaperlessMetadata(w http.ResponseWriter, r *http.Request) {
	if !s.requirePaperless(w) {
		return
	}
	m, err := s.paperless.Metadata(r.Context())
	if err != nil {
		paperlessError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, m)
}

type sendRequest struct {
	Pages         []combinePageRef `json:"pages"`
	Title         string           `json:"title"`
	Created       string           `json:"created"` // YYYY-MM-DD, or empty to let Paperless decide
	Correspondent int              `json:"correspondent"`
	DocumentType  int              `json:"documentType"`
	Tags          []int            `json:"tags"`
}

type sendResponse struct {
	Title  string `json:"title"`
	TaskID string `json:"taskId"`
}

// handlePaperlessSend turns the selected pages into one PDF and uploads it.
// Zero IDs and an empty date leave those choices to Paperless's own
// matching rules. Each scan the pages came from records what was sent, so
// it can show which of its pages are already filed.
func (s *Server) handlePaperlessSend(w http.ResponseWriter, r *http.Request) {
	if !s.requirePaperless(w) {
		return
	}
	var req sendRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	if len(req.Pages) == 0 {
		writeError(w, http.StatusBadRequest, "bad_request", "no pages selected")
		return
	}
	if req.Created != "" {
		if _, err := time.Parse("2006-01-02", req.Created); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "created must be a date like 2026-11-17")
			return
		}
	}
	for _, ref := range req.Pages {
		if s.isScanning(ref.JobID) {
			writeError(w, http.StatusConflict, "scan_running", fmt.Sprintf("scan %q is still running", ref.JobID))
			return
		}
	}

	images, lerr := s.loadPageImages(req.Pages)
	if lerr != nil {
		writeError(w, lerr.status, lerr.code, lerr.message)
		return
	}
	pdf, err := pdfexport.CombinePagesPDF(images, pdfexport.FormatJPEG)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}

	title := strings.TrimSpace(req.Title)
	if title == "" {
		if meta, err := s.store.LoadMeta(req.Pages[0].JobID); err == nil {
			title = meta.Name
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), uploadTimeout)
	defer cancel()
	taskID, err := s.paperless.Upload(ctx, paperless.Document{
		PDF: pdf, Filename: pdfFilename(title), Title: title, Created: req.Created,
		Correspondent: req.Correspondent, DocumentType: req.DocumentType, Tags: req.Tags,
	})
	if err != nil {
		paperlessError(w, err)
		return
	}

	// Record the upload on every scan it drew from, with that scan's own
	// pages. Failing to save a record doesn't undo an upload that worked.
	pagesByJob := map[string][]int{}
	var jobOrder []string
	for _, ref := range req.Pages {
		if _, seen := pagesByJob[ref.JobID]; !seen {
			jobOrder = append(jobOrder, ref.JobID)
		}
		pagesByJob[ref.JobID] = append(pagesByJob[ref.JobID], ref.Page)
	}
	now := time.Now()
	for _, jobID := range jobOrder {
		meta, err := s.store.LoadMeta(jobID)
		if err != nil {
			continue
		}
		meta.Sent = append(meta.Sent, storage.SentDocument{
			Title: title, Pages: pagesByJob[jobID], TaskID: taskID, Status: storage.SentQueued, SentAt: now,
		})
		_ = s.store.SaveMeta(meta)
	}
	writeJSON(w, http.StatusAccepted, sendResponse{Title: title, TaskID: taskID})
}

// handleRefreshSent asks Paperless how each of a scan's pending uploads is
// doing and records the answer. It answers with the updated scan.
func (s *Server) handleRefreshSent(w http.ResponseWriter, r *http.Request) {
	if !s.requirePaperless(w) {
		return
	}
	meta, err := s.store.LoadMeta(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "job not found")
		return
	}
	changed := false
	var lookupErr error
	for i := range meta.Sent {
		d := &meta.Sent[i]
		if d.TaskID == "" || (d.Status != storage.SentQueued && d.Status != storage.SentProcessing) {
			continue
		}
		ctx, cancel := context.WithTimeout(r.Context(), refreshTimeout)
		task, err := s.paperless.Task(ctx, d.TaskID)
		cancel()
		if err != nil {
			// One upload's lookup failing mustn't discard what the others
			// resolved: keep going, and report the failure only if nothing
			// could be learned at all.
			if lookupErr == nil {
				lookupErr = err
			}
			continue
		}
		if task.Status != d.Status || task.DocumentID != d.DocumentID {
			d.Status, d.DocumentID, changed = task.Status, task.DocumentID, true
		}
		if task.Status == paperless.TaskFailed && d.Error != task.Message {
			d.Error, changed = task.Message, true
		}
	}
	if changed {
		if err := s.store.SaveMeta(meta); err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
			return
		}
	}
	if lookupErr != nil && !changed {
		paperlessError(w, lookupErr)
		return
	}
	writeJSON(w, http.StatusOK, meta)
}

// pdfFilename is a safe filename for a title.
func pdfFilename(title string) string {
	var b strings.Builder
	for _, r := range title {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == ' ':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	name := strings.TrimSpace(b.String())
	if name == "" {
		name = "scan"
	}
	return name + ".pdf"
}
