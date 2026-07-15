package api

import (
	"bytes"
	"encoding/json"
	"image"
	"image/png"
	"net/http"

	"github.com/andiapps-dev/doxie-scanner/internal/doxiedx400"
	"github.com/andiapps-dev/doxie-scanner/internal/storage"
)

// resolvePageFile locates a job/page combination and returns the
// metadata plus the on-disk filename to read/write, or writes an error
// response and returns ok=false.
func (s *Server) resolvePageFile(w http.ResponseWriter, r *http.Request) (jobID string, meta storage.JobMeta, pageMeta storage.PageMeta, filename string, ok bool) {
	jobID = r.PathValue("id")
	n, valid := parsePageNumber(r)
	if !valid {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid page number")
		return
	}

	var err error
	meta, err = s.store.LoadMeta(jobID)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "job not found")
		return
	}
	pageMeta, found := findPage(meta, n)
	if !found {
		writeError(w, http.StatusNotFound, "not_found", "page not found")
		return
	}

	filename = pageMeta.File
	ok = true
	return
}

// applyEdit loads a page's stored PNG, runs edit against it, saves the
// result back over the same file, and updates the stored width/height if
// they changed (e.g. a 90/270 degree rotation).
func (s *Server) applyEdit(w http.ResponseWriter, r *http.Request, edit func(image.Image) (*image.NRGBA, error)) {
	jobID, meta, pageMeta, filename, ok := s.resolvePageFile(w, r)
	if !ok {
		return
	}

	data, err := s.store.LoadPageFile(jobID, filename)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "stored page is not a valid PNG: "+err.Error())
		return
	}

	edited, err := edit(img)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, edited); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	if err := s.store.SavePageFile(jobID, filename, buf.Bytes()); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}

	for i := range meta.Pages {
		if meta.Pages[i].Index == pageMeta.Index {
			meta.Pages[i].WidthPx = edited.Bounds().Dx()
			meta.Pages[i].HeightPx = edited.Bounds().Dy()
		}
	}
	if err := s.store.SaveMeta(meta); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

type rotateRequest struct {
	Degrees int `json:"degrees"`
}

// handleRotatePage rotates a page's stored image clockwise by 90, 180,
// or 270 degrees, in place (no undo/version history).
func (s *Server) handleRotatePage(w http.ResponseWriter, r *http.Request) {
	var req rotateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	s.applyEdit(w, r, func(img image.Image) (*image.NRGBA, error) {
		return doxiedx400.Rotate(img, req.Degrees)
	})
}

type cropRequest struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

// handleCropPage crops a page's stored image to the given rectangle, in
// place.
func (s *Server) handleCropPage(w http.ResponseWriter, r *http.Request) {
	var req cropRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	rect := image.Rect(req.X, req.Y, req.X+req.Width, req.Y+req.Height)
	s.applyEdit(w, r, func(img image.Image) (*image.NRGBA, error) {
		return doxiedx400.Crop(img, rect)
	})
}
