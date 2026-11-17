package api

import (
	"bytes"
	"image/png"
	"net/http"

	"github.com/andiapps-dev/doxie-scanner/internal/scanjobs"
)

// handleAutoCrop trims every page of a finished scan to its sheet and
// straightens it, as a scan started with auto-crop does: for scans made
// without it. Pages that fill the scan are left alone, and each page it
// changes keeps its as-scanned image for "restore original". It answers
// with the updated scan.
func (s *Server) handleAutoCrop(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	meta, err := s.store.LoadMeta(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "job not found")
		return
	}
	if s.refuseWhileScanning(w, id) {
		return
	}
	if _, err := scanjobs.AutoCrop(s.store, &meta); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	if err := s.store.SaveMeta(meta); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, meta)
}

// handleRestorePage puts a page back exactly as it was scanned, undoing an
// automatic crop and anything done to the page since. Only a page that was
// changed automatically has its original kept; others get a 404.
func (s *Server) handleRestorePage(w http.ResponseWriter, r *http.Request) {
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
	idx := -1
	for i, p := range meta.Pages {
		if p.Index == n {
			idx = i
		}
	}
	if idx < 0 {
		writeError(w, http.StatusNotFound, "not_found", "page not found")
		return
	}
	page := &meta.Pages[idx]
	if page.Original == "" {
		writeError(w, http.StatusNotFound, "no_original", "this page was not changed automatically, so there is no original to restore")
		return
	}
	data, err := s.store.LoadPageFile(id, page.Original)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "the kept original is not a valid PNG: "+err.Error())
		return
	}
	if err := s.store.SavePageFile(id, page.File, data); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	// The page is as scanned again, so the kept copy has done its job.
	_ = s.store.DeletePageFile(id, page.Original)
	page.Original, page.AutoCrop, page.AutoRotated = "", nil, 0
	page.WidthPx, page.HeightPx = cfg.Width, cfg.Height
	if err := s.store.SaveMeta(meta); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, meta)
}
