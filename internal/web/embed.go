// Package web embeds the frontend (HTML/CSS/JS plus vendored
// Bootstrap/Bootswatch/Cropper.js) directly into the compiled binary, so
// the application is a single self-contained executable with no separate
// static-file directory to ship alongside it — consistent with this
// project's standalone requirement.
package web

import (
	"embed"
	"io/fs"
)

//go:embed static
var files embed.FS

// FS returns the embedded static file tree rooted so that "index.html"
// (not "static/index.html") is a top-level entry — what internal/api's
// http.FileServer expects to serve at "/".
func FS() fs.FS {
	sub, err := fs.Sub(files, "static")
	if err != nil {
		// Only possible if the //go:embed directive above is wrong,
		// which would fail the build itself, not run to this point.
		panic(err)
	}
	return sub
}
