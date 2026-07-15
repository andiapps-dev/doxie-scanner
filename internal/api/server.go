package api

import (
	"io/fs"
	"net/http"

	"github.com/andiapps-dev/doxie-scanner/internal/driver"
	"github.com/andiapps-dev/doxie-scanner/internal/scanjobs"
	"github.com/andiapps-dev/doxie-scanner/internal/storage"
)

// Server wires driver/scanjobs/storage together behind an HTTP API and
// serves the embedded frontend for everything else. It holds no other
// state — handlers are just adapters over the packages that do the real
// work.
type Server struct {
	drv   driver.Driver
	jobs  *scanjobs.Manager
	store *storage.Store
	mux   *http.ServeMux
}

// NewServer builds a Server and wires its full route table. staticFS
// serves the embedded frontend (internal/web) at "/"; it may be nil in
// tests that only care about the JSON API.
func NewServer(drv driver.Driver, jobs *scanjobs.Manager, store *storage.Store, staticFS fs.FS) *Server {
	s := &Server{drv: drv, jobs: jobs, store: store}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/scanner/status", s.handleScannerStatus)

	mux.HandleFunc("POST /api/scans", s.handleStartScan)
	mux.HandleFunc("GET /api/scans", s.handleListScans)
	mux.HandleFunc("GET /api/scans/{id}", s.handleGetScan)
	mux.HandleFunc("PATCH /api/scans/{id}", s.handleRenameScan)
	mux.HandleFunc("DELETE /api/scans/{id}", s.handleDeleteScan)

	mux.HandleFunc("PATCH /api/scans/{id}/pages/order", s.handleReorderPages)
	mux.HandleFunc("GET /api/scans/{id}/pages/{n}", s.handleGetPage)
	mux.HandleFunc("DELETE /api/scans/{id}/pages/{n}", s.handleDeletePage)
	mux.HandleFunc("POST /api/scans/{id}/pages/{n}/rotate", s.handleRotatePage)
	mux.HandleFunc("POST /api/scans/{id}/pages/{n}/crop", s.handleCropPage)
	mux.HandleFunc("GET /api/scans/{id}/pages/{n}/export", s.handleExportPage)

	mux.HandleFunc("POST /api/export/combine", s.handleCombine)

	if staticFS != nil {
		mux.Handle("/", http.FileServer(http.FS(staticFS)))
	}

	s.mux = mux
	return s
}

// ServeHTTP makes Server itself an http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}
