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

	id, err := s.jobs.StartScan(req.Duplex)
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
}

func toSummary(j storage.JobMeta) jobSummary {
	return jobSummary{
		ID:        j.ID,
		Name:      j.Name,
		CreatedAt: j.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		Status:    j.Status,
		PageCount: j.PageCount,
		Duplex:    j.Duplex,
	}
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

// handleDeleteScan deletes a job and all of its pages.
func (s *Server) handleDeleteScan(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.store.LoadMeta(id); err != nil {
		writeError(w, http.StatusNotFound, "not_found", "job not found")
		return
	}
	if err := s.store.DeleteJob(id); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
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

	renumbered := make([]storage.PageMeta, len(pages))
	for i, p := range pages {
		newIndex := i + 1
		newFile := storage.PageFilename(newIndex)
		if p.File != newFile {
			tempFile := ".renumber-" + p.File
			if err := s.store.RenamePageFile(jobID, p.File, tempFile); err != nil {
				return nil, err
			}
			pending = append(pending, pendingRename{tempFile: tempFile, newFile: newFile})
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
