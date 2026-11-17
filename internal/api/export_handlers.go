package api

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"net/http"
	"strconv"
	"strings"

	"github.com/andiapps-dev/doxie-scanner/internal/pdfexport"
)

// handleExportPage exports one stored page as PNG, JPEG, or a single-page
// PDF, with a Content-Disposition attachment header so a browser download
// gets a sensible filename.
func (s *Server) handleExportPage(w http.ResponseWriter, r *http.Request) {
	jobID, _, pageMeta, filename, ok := s.resolvePageFile(w, r)
	if !ok {
		return
	}

	data, err := s.store.LoadPageFile(jobID, filename)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}

	format := r.URL.Query().Get("format")
	if format == "" {
		format = "png"
	}

	base := fmt.Sprintf("%s-page-%03d", jobID, pageMeta.Index)

	switch format {
	case "png":
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", base+".png"))
		w.Write(data)

	case "jpg", "jpeg":
		img, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
			return
		}
		// Defaults to 100 — deliberately higher than pdfexport.JPEGQuality
		// (90): this is the "I want the best quality" choice. The
		// frontend's "JPEG (smaller)" option explicitly requests
		// ?quality=90 (the same tier PDF export uses) for anyone who
		// wants a compact JPEG without wrapping it in a PDF. Anything
		// outside 1-100, or missing, falls back to the 100 default rather
		// than erroring, matching how an empty "format" already defaults
		// to png above.
		quality := 100
		if q, err := strconv.Atoi(r.URL.Query().Get("quality")); err == nil && q >= 1 && q <= 100 {
			quality = q
		}
		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", base+".jpg"))
		w.Write(buf.Bytes())

	case "pdf":
		img, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
			return
		}
		// No format choice here (unlike handleCombine): this is a single
		// page, and anyone who wants lossless already has the plain PNG
		// download right next to this one in the same export menu, so
		// this PDF option can just default to the smaller JPEG embed.
		pdfBytes, err := pdfexport.SinglePagePDF(img, pdfexport.FormatJPEG)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/pdf")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", base+".pdf"))
		w.Write(pdfBytes)

	default:
		writeError(w, http.StatusBadRequest, "bad_request", "unsupported format (use png, jpg, or pdf)")
	}
}

type combinePageRef struct {
	JobID string `json:"jobId"`
	Page  int    `json:"page"`
}

type combineRequest struct {
	Pages       []combinePageRef `json:"pages"`
	Title       string           `json:"title,omitempty"`
	ImageFormat string           `json:"imageFormat,omitempty"`
}

// handleCombine assembles pages from any number of past jobs, in the
// requested order, into one combined PDF.
func (s *Server) handleCombine(w http.ResponseWriter, r *http.Request) {
	var req combineRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	if len(req.Pages) == 0 {
		writeError(w, http.StatusBadRequest, "bad_request", "no pages specified")
		return
	}

	// Default to JPEG (smaller, no visible difference for typical text
	// scans) — PNG is the deliberate opt-in for photo/art content where
	// JPEG artifacts would show.
	format := pdfexport.FormatJPEG
	switch req.ImageFormat {
	case "", "jpeg":
		format = pdfexport.FormatJPEG
	case "png":
		format = pdfexport.FormatPNG
	default:
		writeError(w, http.StatusBadRequest, "bad_request", "imageFormat must be \"png\" or \"jpeg\"")
		return
	}

	images, lerr := s.loadPageImages(req.Pages)
	if lerr != nil {
		writeError(w, lerr.status, lerr.code, lerr.message)
		return
	}

	pdfBytes, err := pdfexport.CombinePagesPDF(images, format)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}

	title := req.Title
	if title == "" {
		title = "combined"
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", title+".pdf"))
	w.Write(pdfBytes)
}

type imagesRequest struct {
	Pages   []combinePageRef `json:"pages"`
	Title   string           `json:"title,omitempty"`
	Format  string           `json:"format"`
	Quality int              `json:"quality,omitempty"`
}

// safeName reduces a user-typed title to something safe as a file name.
func safeName(title, fallback string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(title) {
		switch {
		case r < 0x20, strings.ContainsRune(`/\:*?"<>|`, r):
			b.WriteRune('-')
		default:
			b.WriteRune(r)
		}
	}
	name := strings.Trim(b.String(), ". ")
	if name == "" {
		return fallback
	}
	return name
}

// handleExportImages streams the selected pages, in the order given, as a
// ZIP of separate PNG or JPEG files. Every reference is checked before the
// first byte is written, and pages are processed one at a time so memory
// stays flat however many are selected.
func (s *Server) handleExportImages(w http.ResponseWriter, r *http.Request) {
	var req imagesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	if len(req.Pages) == 0 {
		writeError(w, http.StatusBadRequest, "bad_request", "no pages specified")
		return
	}
	ext := ""
	switch req.Format {
	case "png":
		ext = "png"
	case "jpeg", "jpg":
		ext = "jpg"
	default:
		writeError(w, http.StatusBadRequest, "bad_request", "format must be \"png\" or \"jpeg\"")
		return
	}
	quality := 90
	if req.Quality != 0 {
		if req.Quality < 1 || req.Quality > 100 {
			writeError(w, http.StatusBadRequest, "bad_request", "quality must be 1-100")
			return
		}
		quality = req.Quality
	}

	files := make([]string, len(req.Pages))
	for i, ref := range req.Pages {
		meta, err := s.store.LoadMeta(ref.JobID)
		if err != nil {
			writeError(w, http.StatusNotFound, "not_found", fmt.Sprintf("job %q not found", ref.JobID))
			return
		}
		pm, found := findPage(meta, ref.Page)
		if !found {
			writeError(w, http.StatusNotFound, "not_found", fmt.Sprintf("page %d not found in job %q", ref.Page, ref.JobID))
			return
		}
		files[i] = pm.File
	}

	title := safeName(req.Title, "pages")
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", title+".zip"))
	zw := zip.NewWriter(w)
	defer zw.Close()
	for i, ref := range req.Pages {
		data, err := s.store.LoadPageFile(ref.JobID, files[i])
		if err == nil && ext == "jpg" {
			var img image.Image
			if img, err = png.Decode(bytes.NewReader(data)); err == nil {
				var buf bytes.Buffer
				if err = jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err == nil {
					data = buf.Bytes()
				}
			}
		}
		if err != nil {
			// Headers are gone; abort the connection so the client sees a
			// failed download rather than a silently short archive.
			panic(http.ErrAbortHandler)
		}
		// Images are already compressed; storing avoids wasted CPU.
		fw, err := zw.CreateHeader(&zip.FileHeader{Name: fmt.Sprintf("%s-%03d.%s", title, i+1, ext), Method: zip.Store})
		if err != nil {
			panic(http.ErrAbortHandler)
		}
		if _, err := fw.Write(data); err != nil {
			return
		}
	}
}

// loadError is a failure to load a selected page, with the HTTP response
// that describes it.
type loadError struct {
	status  int
	code    string
	message string
}

// loadPageImages decodes the stored image of each referenced page, in the
// order given. It is how a combined PDF and a Paperless upload read the
// same selection.
func (s *Server) loadPageImages(refs []combinePageRef) ([]image.Image, *loadError) {
	images := make([]image.Image, 0, len(refs))
	for _, ref := range refs {
		meta, err := s.store.LoadMeta(ref.JobID)
		if err != nil {
			return nil, &loadError{http.StatusNotFound, "not_found", fmt.Sprintf("job %q not found", ref.JobID)}
		}
		pageMeta, found := findPage(meta, ref.Page)
		if !found {
			return nil, &loadError{http.StatusNotFound, "not_found", fmt.Sprintf("page %d not found in job %q", ref.Page, ref.JobID)}
		}
		data, err := s.store.LoadPageFile(ref.JobID, pageMeta.File)
		if err != nil {
			return nil, &loadError{http.StatusInternalServerError, "internal_error", err.Error()}
		}
		img, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			return nil, &loadError{http.StatusInternalServerError, "internal_error", err.Error()}
		}
		images = append(images, img)
	}
	return images, nil
}
