package api

import (
	"bytes"
	"fmt"
	"image/jpeg"
	"image/png"
	"net/http"
	"os"

	"github.com/disintegration/imaging"
)

// Thumbnails are what the page grid and the selection bar show. The stored
// pages are full-resolution PNGs of 5 to 14 MB each, which is too much to
// load a dozen of at once, least of all over a phone's connection.
const (
	thumbMaxW, thumbMaxH = 360, 480
	thumbQuality         = 80
)

// thumbSlots caps how many pages are decoded at once: the browser asks for
// a whole grid of thumbnails together, and decoding a large PNG takes real
// memory, which a Raspberry Pi has little of.
var thumbSlots = make(chan struct{}, 2)

// handleGetThumb serves a small JPEG of one page, made from the stored page
// the first time it is asked for and then kept on disk. The cached file's
// name carries the page file's size and modified time, so an edited page
// gets a new thumbnail and an old one can never be served for it.
func (s *Server) handleGetThumb(w http.ResponseWriter, r *http.Request) {
	jobID, _, _, filename, ok := s.resolvePageFile(w, r)
	if !ok {
		return
	}
	info, err := os.Stat(s.store.PageFilePath(jobID, filename))
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "page not found")
		return
	}
	prefix := filename + "."
	name := fmt.Sprintf("%s%d-%d.jpg", prefix, info.Size(), info.ModTime().UnixNano())

	data, err := s.store.LoadThumb(jobID, name)
	if err != nil {
		if data, err = s.makeThumb(jobID, filename); err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
			return
		}
		_ = s.store.SaveThumb(jobID, name, prefix, data) // only a cache: serve it either way
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "private, no-cache") // revalidate, which is cheap: it's a 304
	http.ServeContent(w, r, "thumb.jpg", info.ModTime(), bytes.NewReader(data))
}

func (s *Server) makeThumb(jobID, filename string) ([]byte, error) {
	thumbSlots <- struct{}{}
	defer func() { <-thumbSlots }()

	raw, err := s.store.LoadPageFile(jobID, filename)
	if err != nil {
		return nil, err
	}
	img, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("stored page is not a valid PNG: %w", err)
	}
	small := imaging.Fit(img, thumbMaxW, thumbMaxH, imaging.Linear)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, small, &jpeg.Options{Quality: thumbQuality}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
