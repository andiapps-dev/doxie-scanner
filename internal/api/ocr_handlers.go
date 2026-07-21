package api

import (
	"errors"
	"net/http"

	"github.com/andiapps-dev/doxie-scanner/internal/ocr"
)

// handleExtractText runs OCR on a stored page and returns the recognized
// text. Unlike rotate/crop, this never modifies the stored page — like
// Export, it only derives a representation on demand — so it's a GET,
// and there's no separate toggle for skipping the deskew step: unpaper
// always runs first (see internal/ocr).
func (s *Server) handleExtractText(w http.ResponseWriter, r *http.Request) {
	jobID, _, _, filename, ok := s.resolvePageFile(w, r)
	if !ok {
		return
	}

	path := s.store.PageFilePath(jobID, filename)
	text, err := ocr.ExtractText(path, s.ocrLang)
	if err != nil {
		if errors.Is(err, ocr.ErrToolNotAvailable) {
			writeError(w, http.StatusInternalServerError, "ocr_unavailable", err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"text": text})
}
