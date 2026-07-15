package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/andiapps-dev/doxie-scanner/internal/driver"
	"github.com/andiapps-dev/doxie-scanner/internal/scsiusb"
)

func TestHandleVersion(t *testing.T) {
	drv := &fakeDriver{info: driver.Info{Name: "doxie-dx400"}}
	srv, _ := newTestServer(t, drv)

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/version", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var resp versionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Version != "test" {
		t.Errorf("Version = %q, want %q (from newTestServer)", resp.Version, "test")
	}
}

func TestHandleScannerStatus_Connected(t *testing.T) {
	drv := &fakeDriver{info: driver.Info{Name: "doxie-dx400", VID: 0x2740, PID: 0x000c}}
	srv, _ := newTestServer(t, drv)

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/scanner/status", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var resp scannerStatusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.Connected {
		t.Error("expected connected=true")
	}
	if resp.VID != "0x2740" || resp.PID != "0x000c" {
		t.Errorf("unexpected vid/pid: %+v", resp)
	}
	if resp.Error != nil {
		t.Errorf("expected no error, got %+v", resp.Error)
	}
}

func TestHandleScannerStatus_DeviceNotFound(t *testing.T) {
	drv := &fakeDriver{
		info:      driver.Info{Name: "doxie-dx400", VID: 0x2740, PID: 0x000c},
		detectErr: &scsiusb.ErrDeviceNotFound{VID: 0x2740, PID: 0x000c},
	}
	srv, _ := newTestServer(t, drv)

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/scanner/status", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (not-connected is a normal poll result)", rec.Code)
	}
	var resp scannerStatusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Connected {
		t.Error("expected connected=false")
	}
	if resp.Error == nil || resp.Error.Code != "device_not_found" {
		t.Errorf("expected device_not_found error, got %+v", resp.Error)
	}
}

func TestHandleScannerStatus_ClaimInterfaceFailed(t *testing.T) {
	drv := &fakeDriver{
		info:      driver.Info{Name: "doxie-dx400"},
		detectErr: &scsiusb.ErrClaimInterface{Cause: errors.New("permission denied")},
	}
	srv, _ := newTestServer(t, drv)

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/scanner/status", nil))

	var resp scannerStatusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Error == nil || resp.Error.Code != "claim_interface_failed" {
		t.Errorf("expected claim_interface_failed error, got %+v", resp.Error)
	}
}

func TestHandleScannerStatus_GenericScsiError(t *testing.T) {
	drv := &fakeDriver{
		info:      driver.Info{Name: "doxie-dx400"},
		detectErr: &scsiusb.ScsiError{Opcode: 0x1b, Message: "boom"},
	}
	srv, _ := newTestServer(t, drv)

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/scanner/status", nil))

	var resp scannerStatusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Error == nil || resp.Error.Code != "scsi_error" {
		t.Errorf("expected scsi_error, got %+v", resp.Error)
	}
}
