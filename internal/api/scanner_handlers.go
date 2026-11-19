package api

import (
	"fmt"
	"net/http"

	"github.com/andiapps-dev/doxie-scanner/internal/storage"
)

type versionResponse struct {
	Version string `json:"version"`
}

// handleVersion reports the build-time version identifier — a git tag in
// release builds, "dev" for a local/untagged build. Useful for support:
// confirming exactly what a running container is.
func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, versionResponse{Version: s.version})
}

type scannerStatusResponse struct {
	Connected bool   `json:"connected"`
	VID       string `json:"vid"`
	PID       string `json:"pid"`
	Driver    string `json:"driver"`
	// AutoRotate is true when pages can be turned upright from their text.
	AutoRotate bool `json:"autoRotate"`
	// AutoCrop is always true: cropping needs nothing installed.
	AutoCrop bool `json:"autoCrop"`
	// PaperlessURL is set when sending to Paperless is configured; the UI
	// uses it to link to filed documents.
	PaperlessURL string    `json:"paperlessUrl,omitempty"`
	Error        *apiError `json:"error,omitempty"`
	// ScanningJobID is set while a scan is running, whoever started it (this
	// page, another browser, or the scanner's own button), so a page that
	// did not start it can show its progress.
	ScanningJobID string `json:"scanningJobId,omitempty"`
}

// handleScannerStatus reports whether the scanner is currently reachable.
// It always responds 200 — "not connected" is a normal, expected state
// for this endpoint (the frontend polls it every few seconds for a live
// badge), not an HTTP-level failure.
func (s *Server) handleScannerStatus(w http.ResponseWriter, r *http.Request) {
	info := s.drv.Info()
	resp := scannerStatusResponse{
		VID:    fmt.Sprintf("0x%04x", info.VID),
		PID:    fmt.Sprintf("0x%04x", info.PID),
		Driver: info.Name,
		// Auto-rotate needs tesseract's orientation data; auto-crop is pure Go.
		AutoRotate: s.jobs.CanAutoRotate(),
		AutoCrop:   true,
	}
	if s.paperless != nil {
		resp.PaperlessURL = s.paperless.BaseURL()
	}

	if cur := s.jobs.CurrentJob(); cur != nil && cur.Status == storage.StatusRunning {
		resp.ScanningJobID = cur.ID
	}

	if err := s.drv.Detect(r.Context()); err != nil {
		_, code, message := classifyDeviceError(err)
		resp.Error = &apiError{Code: code, Message: message}
	} else {
		resp.Connected = true
	}

	writeJSON(w, http.StatusOK, resp)
}
