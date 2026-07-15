// Package api is the HTTP layer: a stdlib net/http.ServeMux exposing a
// small JSON REST API (no auth — this is a LAN utility, not a
// multi-tenant service) plus the embedded frontend. Handlers never talk
// to scsiusb or doxiedx400 directly — only through driver.Driver,
// scanjobs.Manager, and storage.Store — so this package doesn't care
// which scanner model is active.
package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/andiapps-dev/doxie-scanner/internal/scsiusb"
)

// apiError is the JSON shape of every error this API returns.
type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type errorEnvelope struct {
	Error apiError `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, errorEnvelope{Error: apiError{Code: code, Message: message}})
}

// classifyDeviceError maps a driver/scsiusb error to an HTTP status and
// a stable machine-readable code, with a plain-language message —
// exactly the three specific error classes called for in this project's
// error-detection requirement: device not found, interface claim
// failure, and any other transport-level SCSI error.
func classifyDeviceError(err error) (status int, code, message string) {
	var notFound *scsiusb.ErrDeviceNotFound
	if errors.As(err, &notFound) {
		return http.StatusServiceUnavailable, "device_not_found", err.Error()
	}
	var claimErr *scsiusb.ErrClaimInterface
	if errors.As(err, &claimErr) {
		return http.StatusServiceUnavailable, "claim_interface_failed", err.Error()
	}
	return http.StatusInternalServerError, "scsi_error", err.Error()
}
