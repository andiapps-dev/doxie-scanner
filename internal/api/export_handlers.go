package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"net/http"

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
		var buf bytes.Buffer
		// Quality 100 here is deliberately higher than the JPEG quality
		// PDFs embed at (see pdfexport.jpegQuality): this standalone
		// download is the "I want the best quality" choice, since PDF
		// export is the "I want it smaller" choice.
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 100}); err != nil {
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

	images := make([]image.Image, 0, len(req.Pages))
	for _, ref := range req.Pages {
		meta, err := s.store.LoadMeta(ref.JobID)
		if err != nil {
			writeError(w, http.StatusNotFound, "not_found", fmt.Sprintf("job %q not found", ref.JobID))
			return
		}
		pageMeta, found := findPage(meta, ref.Page)
		if !found {
			writeError(w, http.StatusNotFound, "not_found", fmt.Sprintf("page %d not found in job %q", ref.Page, ref.JobID))
			return
		}

		data, err := s.store.LoadPageFile(ref.JobID, pageMeta.File)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
			return
		}
		img, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
			return
		}
		images = append(images, img)
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
