package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/andiapps-dev/doxie-scanner/internal/scanjobs"
	"github.com/andiapps-dev/doxie-scanner/internal/storage"
)

type startScanRequest struct {
	Duplex bool `json:"duplex"`
	// AutoRotate turns each page upright after the scan, and AutoCrop trims
	// each page to its sheet and straightens it. Both are off unless asked
	// for (the web app asks for both by default), so a script that sends
	// only {"duplex": true} keeps getting the pages exactly as scanned. A
	// true auto-rotate is ignored when the server has no orientation
	// detector.
	AutoRotate bool `json:"autoRotate"`
	AutoCrop   bool `json:"autoCrop"`
}

type startScanResponse struct {
	JobID  string `json:"jobId"`
	Status string `json:"status"`
}

// handleStartScan begins a new scan job. A missing or empty body is
// treated as {"duplex":false} rather than a bad request — duplex is an
// opt-in, not a required field.
func (s *Server) handleStartScan(w http.ResponseWriter, r *http.Request) {
	var req startScanRequest
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
			return
		}
	}

	id, err := s.jobs.StartScanWith(scanjobs.Options{
		Duplex:     req.Duplex,
		AutoRotate: req.AutoRotate,
		AutoCrop:   req.AutoCrop,
	})
	if err != nil {
		if errors.Is(err, scanjobs.ErrAlreadyRunning) {
			jobID := ""
			if current := s.jobs.CurrentJob(); current != nil {
				jobID = current.ID
			}
			writeJSON(w, http.StatusConflict, startScanResponse{JobID: jobID, Status: "running"})
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}

	writeJSON(w, http.StatusAccepted, startScanResponse{JobID: id, Status: "running"})
}

type jobSummary struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	CreatedAt string            `json:"createdAt"`
	Status    storage.JobStatus `json:"status"`
	PageCount int               `json:"pageCount"`
	Duplex    bool              `json:"duplex"`
	// UnsentPages is how many of the scan's pages are in no Paperless
	// document that went through, so the UI can warn before deleting a scan
	// whose pages never reached Paperless.
	UnsentPages int `json:"unsentPages"`
}

func toSummary(j storage.JobMeta) jobSummary {
	sum := jobSummary{
		ID:        j.ID,
		Name:      j.Name,
		CreatedAt: j.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		Status:    j.Status,
		PageCount: j.PageCount,
		Duplex:    j.Duplex,
	}
	sum.UnsentPages = unsentPages(j)
	return sum
}

// handleListScans lists every job, newest first (storage.Store.ListJobs
// already sorts them that way).
func (s *Server) handleListScans(w http.ResponseWriter, r *http.Request) {
	jobs, err := s.store.ListJobs()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	summaries := make([]jobSummary, 0, len(jobs))
	for _, j := range jobs {
		summaries = append(summaries, toSummary(j))
	}
	writeJSON(w, http.StatusOK, summaries)
}

type jobDetailResponse struct {
	storage.JobMeta
	PagesScanned *int `json:"pagesScanned,omitempty"`
	// Stage, StageDone and StageTotal say what a running scan is doing
	// right now (see scanjobs.Stage*) and how far along it is.
	Stage      string `json:"stage,omitempty"`
	StageDone  int    `json:"stageDone,omitempty"`
	StageTotal int    `json:"stageTotal,omitempty"`
}

// handleGetScan returns a job's full metadata. While it's the currently
// running job, it also overlays live in-memory progress from
// scanjobs.Manager, since meta.json's own PageCount is only updated at
// best-effort checkpoints during the scan.
func (s *Server) handleGetScan(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	meta, err := s.store.LoadMeta(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "job not found")
		return
	}

	resp := jobDetailResponse{JobMeta: meta}
	if current := s.jobs.CurrentJob(); current != nil && current.ID == id && current.Status == storage.StatusRunning {
		n := current.PagesScanned
		resp.PagesScanned = &n
		resp.Stage, resp.StageDone, resp.StageTotal = current.Stage, current.StageDone, current.StageTotal
	}
	writeJSON(w, http.StatusOK, resp)
}

type renameRequest struct {
	Name string `json:"name"`
}

// handleRenameScan changes a job's display name. The auto-generated name
// set at creation time is only ever a default.
func (s *Server) handleRenameScan(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if s.refuseWhileScanning(w, id) {
		return
	}

	var req renameRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "name must not be empty")
		return
	}

	meta, err := s.store.LoadMeta(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "job not found")
		return
	}
	meta.Name = name
	if err := s.store.SaveMeta(meta); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, meta)
}

// deleteOutcome is how deleting one scan went.
type deleteOutcome int

const (
	deleted deleteOutcome = iota
	deleteNotFound
	deleteRunning
	deleteFailed
)

// deleteScan removes a scan and all its pages. A scan that is still
// running is refused: the scanner is writing into its folder, and pulling
// that out from under it would corrupt the scan.
func (s *Server) deleteScan(id string) (deleteOutcome, error) {
	if _, err := s.store.LoadMeta(id); err != nil {
		return deleteNotFound, err
	}
	if s.isScanning(id) {
		return deleteRunning, nil
	}
	if err := s.store.DeleteJob(id); err != nil {
		return deleteFailed, err
	}
	return deleted, nil
}

// handleDeleteScan deletes a job and all of its pages.
func (s *Server) handleDeleteScan(w http.ResponseWriter, r *http.Request) {
	switch outcome, err := s.deleteScan(r.PathValue("id")); outcome {
	case deleteNotFound:
		writeError(w, http.StatusNotFound, "not_found", "job not found")
	case deleteRunning:
		writeError(w, http.StatusConflict, "scan_running", "this scan is still running")
	case deleteFailed:
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

type bulkDeleteRequest struct {
	IDs []string `json:"ids"`
}

type skippedScan struct {
	ID     string `json:"id"`
	Reason string `json:"reason"` // not_found, scan_running or internal_error
}

type bulkDeleteResponse struct {
	Deleted []string      `json:"deleted"`
	Skipped []skippedScan `json:"skipped"`
}

// handleBulkDeleteScans deletes every scan in ids. One that can't be
// deleted (already gone, still running, or failing on disk) is reported in
// skipped and does not stop the rest, so a long selection is never half
// done by surprise: the answer says exactly what happened to each.
func (s *Server) handleBulkDeleteScans(w http.ResponseWriter, r *http.Request) {
	var req bulkDeleteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	if len(req.IDs) == 0 {
		writeError(w, http.StatusBadRequest, "bad_request", "no scans selected")
		return
	}
	resp := bulkDeleteResponse{Deleted: []string{}, Skipped: []skippedScan{}}
	seen := map[string]bool{}
	for _, id := range req.IDs {
		if seen[id] {
			continue
		}
		seen[id] = true
		switch outcome, _ := s.deleteScan(id); outcome {
		case deleted:
			resp.Deleted = append(resp.Deleted, id)
		case deleteNotFound:
			resp.Skipped = append(resp.Skipped, skippedScan{id, "not_found"})
		case deleteRunning:
			resp.Skipped = append(resp.Skipped, skippedScan{id, "scan_running"})
		default:
			resp.Skipped = append(resp.Skipped, skippedScan{id, "internal_error"})
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

type reorderPagesRequest struct {
	Order []int `json:"order"`
}

// handleReorderPages changes the display/export order of a job's pages
// without renumbering them — Order must list every existing page Index
// exactly once, in the desired new order. Page Index values (and thus
// their /pages/{n} URLs and on-disk filenames) never change; only their
// position within meta.Pages does.
func (s *Server) handleReorderPages(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if s.refuseWhileScanning(w, id) {
		return
	}

	var req reorderPagesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}

	meta, err := s.store.LoadMeta(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "job not found")
		return
	}

	if len(req.Order) != len(meta.Pages) {
		writeError(w, http.StatusBadRequest, "bad_request", "order must include every page exactly once")
		return
	}
	byIndex := make(map[int]storage.PageMeta, len(meta.Pages))
	for _, p := range meta.Pages {
		byIndex[p.Index] = p
	}
	reordered := make([]storage.PageMeta, 0, len(req.Order))
	seen := make(map[int]bool, len(req.Order))
	for _, idx := range req.Order {
		p, ok := byIndex[idx]
		if !ok || seen[idx] {
			writeError(w, http.StatusBadRequest, "bad_request", "order must be a permutation of the job's existing page numbers")
			return
		}
		seen[idx] = true
		reordered = append(reordered, p)
	}

	meta.Pages = reordered
	if err := s.store.SaveMeta(meta); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, meta)
}

// findPage locates a page by its 1-based index within a job's metadata.
func findPage(meta storage.JobMeta, n int) (storage.PageMeta, bool) {
	for _, p := range meta.Pages {
		if p.Index == n {
			return p, true
		}
	}
	return storage.PageMeta{}, false
}

// parsePageNumber parses the {n} path segment, rejecting anything that
// isn't a positive integer up front so handlers don't need to.
func parsePageNumber(r *http.Request) (int, bool) {
	n, err := strconv.Atoi(r.PathValue("n"))
	if err != nil || n < 1 {
		return 0, false
	}
	return n, true
}

// handleGetPage streams one page's raw stored PNG.
func (s *Server) handleGetPage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	n, ok := parsePageNumber(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid page number")
		return
	}

	meta, err := s.store.LoadMeta(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "job not found")
		return
	}
	pageMeta, ok := findPage(meta, n)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "page not found")
		return
	}

	data, err := s.store.LoadPageFile(id, pageMeta.File)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Write(data)
}

// handleDeletePage removes one page, then renumbers the remaining pages
// to close the gap (deleting page 1 out of [1,2] leaves a single page
// renumbered to 1, not a page still labeled 2). Since duplex sides are
// independent pages (see storage.PageMeta), deleting one never destroys
// any other page's image — only its number and filename may shift.
func (s *Server) handleDeletePage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if s.refuseWhileScanning(w, id) {
		return
	}
	n, ok := parsePageNumber(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid page number")
		return
	}

	meta, err := s.store.LoadMeta(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "job not found")
		return
	}
	pageMeta, ok := findPage(meta, n)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "page not found")
		return
	}

	if err := s.store.DeletePageFile(id, pageMeta.File); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	if pageMeta.Original != "" {
		_ = s.store.DeletePageFile(id, pageMeta.Original) // a missing backup must not stop the delete
	}

	remaining := make([]storage.PageMeta, 0, len(meta.Pages)-1)
	for _, p := range meta.Pages {
		if p.Index != n {
			remaining = append(remaining, p)
		}
	}
	renumbered, err := s.renumberPages(id, remaining)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}

	meta.Pages = renumbered
	meta.PageCount = len(renumbered)
	newIndex := make(map[int]int, len(remaining))
	for i, p := range remaining {
		newIndex[p.Index] = renumbered[i].Index
	}
	meta.Sent = remapSent(meta.Sent, newIndex)
	if err := s.store.SaveMeta(meta); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// renumberPages reassigns sequential 1..N indexes to pages, in their
// current order, renaming on-disk files to match. Renames go through a
// temporary name first (page N's file might need to become page M's
// filename, which could currently belong to a different not-yet-processed
// page) so a mid-sequence rename can never overwrite another page's file
// regardless of processing order.
func (s *Server) renumberPages(jobID string, pages []storage.PageMeta) ([]storage.PageMeta, error) {
	type pendingRename struct {
		tempFile string
		newFile  string
	}
	var pending []pendingRename
	// A page's kept original follows it to its new number.
	move := func(oldFile, newFile string) error {
		tempFile := ".renumber-" + oldFile
		if err := s.store.RenamePageFile(jobID, oldFile, tempFile); err != nil {
			return err
		}
		pending = append(pending, pendingRename{tempFile: tempFile, newFile: newFile})
		return nil
	}

	renumbered := make([]storage.PageMeta, len(pages))
	for i, p := range pages {
		newIndex := i + 1
		newFile := storage.PageFilename(newIndex)
		if p.File != newFile {
			if err := move(p.File, newFile); err != nil {
				return nil, err
			}
		}
		if p.Original != "" {
			newOriginal := storage.OriginalFilename(newIndex)
			if p.Original != newOriginal {
				if err := move(p.Original, newOriginal); err != nil {
					return nil, err
				}
			}
			p.Original = newOriginal
		}
		p.Index = newIndex
		p.File = newFile
		renumbered[i] = p
	}
	for _, op := range pending {
		if err := s.store.RenamePageFile(jobID, op.tempFile, op.newFile); err != nil {
			return nil, err
		}
	}
	return renumbered, nil
}

// remapSent keeps a scan's "sent to Paperless" records pointing at the
// right pages after a deletion renumbered them. newIndex maps each
// surviving page's old index to its new one, so it is right however the
// pages had been reordered; a page that was deleted is absent from it and
// drops out, and a document left with no pages in this scan is dropped.
func remapSent(sent []storage.SentDocument, newIndex map[int]int) []storage.SentDocument {
	var out []storage.SentDocument
	for _, d := range sent {
		var pages []int
		for _, n := range d.Pages {
			if to, ok := newIndex[n]; ok {
				pages = append(pages, to)
			}
		}
		if len(pages) == 0 {
			continue
		}
		d.Pages = pages
		out = append(out, d)
	}
	return out
}

// unsentPages counts the pages in no sent document that hasn't failed.
func unsentPages(j storage.JobMeta) int {
	sent := map[int]bool{}
	for _, d := range j.Sent {
		if d.Status == storage.SentFailed {
			continue
		}
		for _, n := range d.Pages {
			sent[n] = true
		}
	}
	unsent := 0
	for _, p := range j.Pages {
		if !sent[p.Index] {
			unsent++
		}
	}
	return unsent
}
