package web

import "testing"

// TestFS_ContainsExpectedEntries is a cheap sanity check that the
// //go:embed directive and fs.Sub rooting are wired correctly — not part
// of this project's 95% Go-coverage target (the frontend itself is
// explicitly out of scope for that), just a guard against a silently
// broken embed path.
func TestFS_ContainsExpectedEntries(t *testing.T) {
	fsys := FS()
	for _, name := range []string{"index.html", "app.js", "style.css", "vendor/bootstrap.min.css", "vendor/bootstrap.bundle.min.js", "vendor/cropper.min.css", "vendor/cropper.min.js"} {
		if _, err := fsys.Open(name); err != nil {
			t.Errorf("expected embedded file %q, got error: %v", name, err)
		}
	}
}
