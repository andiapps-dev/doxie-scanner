package api

import (
	"fmt"
	"net/http"
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
	Connected bool      `json:"connected"`
	VID       string    `json:"vid"`
	PID       string    `json:"pid"`
	Driver    string    `json:"driver"`
	Error     *apiError `json:"error,omitempty"`
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
	}

	if err := s.drv.Detect(r.Context()); err != nil {
		_, code, message := classifyDeviceError(err)
		resp.Error = &apiError{Code: code, Message: message}
	} else {
		resp.Connected = true
	}

	writeJSON(w, http.StatusOK, resp)
}
