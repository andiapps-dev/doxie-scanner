package api

import (
	"errors"
	"net/http"

	"github.com/andiapps-dev/doxie-scanner/internal/orient"
	"github.com/andiapps-dev/doxie-scanner/internal/scanjobs"
	"github.com/andiapps-dev/doxie-scanner/internal/storage"
)

// isScanning reports whether job id is the scan in progress.
func (s *Server) isScanning(id string) bool {
	cur := s.jobs.CurrentJob()
	return cur != nil && cur.ID == id && cur.Status == storage.StatusRunning
}

// refuseWhileScanning answers 409 and reports true when job id is the scan
// in progress. A scan that is running is not editable: the scan manager
// holds that scan's metadata in memory and writes it back when it finishes
// (after cropping and turning pages, which can take minutes on a slow
// machine), so an edit made meanwhile would be overwritten, or would leave
// the scan listing a page that was deleted.
func (s *Server) refuseWhileScanning(w http.ResponseWriter, id string) bool {
	if !s.isScanning(id) {
		return false
	}
	writeError(w, http.StatusConflict, "scan_running", "this scan is still running")
	return true
}

// handleAutoRotate turns every page of a finished scan upright from the
// text on it, for scans made without auto-rotate or pages that were
// turned the wrong way since. Pages with too little text are left alone.
// It answers with the updated scan.
func (s *Server) handleAutoRotate(w http.ResponseWriter, r *http.Request) {
	orienter := s.jobs.Orienter()
	if orienter == nil {
		writeError(w, http.StatusServiceUnavailable, "auto_rotate_unavailable",
			"Auto-rotate needs tesseract with the osd language data installed.")
		return
	}
	id := r.PathValue("id")
	meta, err := s.store.LoadMeta(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "job not found")
		return
	}
	if s.refuseWhileScanning(w, id) {
		return
	}
	turned, rotateErr := scanjobs.AutoRotate(s.store, orienter, &meta)
	// Pages already rewritten on disk must have their metadata saved even if
	// the run stopped part way (tesseract vanished): otherwise their size
	// and "auto-rotated" mark would no longer match the files.
	if turned > 0 {
		if err := s.store.SaveMeta(meta); err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
			return
		}
	}
	if rotateErr != nil {
		status, code := http.StatusInternalServerError, "internal_error"
		if errors.Is(rotateErr, orient.ErrToolNotAvailable) {
			status, code = http.StatusServiceUnavailable, "auto_rotate_unavailable"
		}
		writeError(w, status, code, rotateErr.Error())
		return
	}
	writeJSON(w, http.StatusOK, meta)
}
