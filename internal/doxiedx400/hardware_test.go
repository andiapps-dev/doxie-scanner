//go:build hardware

package doxiedx400

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/andiapps-dev/doxie-scanner/internal/driver"
)

// TestHardwareScan scans one sheet on a real, connected DX400.
//
//	DX400_RES=300 DX400_DUPLEX=1 DX400_OUT=/tmp/dx400 \
//	  go test -tags hardware -run TestHardwareScan -v ./internal/doxiedx400
func TestHardwareScan(t *testing.T) {
	res, _ := strconv.Atoi(os.Getenv("DX400_RES"))
	duplex := os.Getenv("DX400_DUPLEX") == "1"
	out := os.Getenv("DX400_OUT")
	if out == "" {
		t.Fatal("set DX400_OUT to a directory for the scanned pages")
	}

	opened, err := New().Open(context.Background())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer opened.Close()
	// The scanner's own JPEGs are what is worth saving here, before any
	// decoding, so use the driver's lower layer.
	sess := opened.(*session)

	t.Log("waiting for paper…")
	deadline := time.Now().Add(3 * time.Minute)
	for {
		ok, err := sess.HasNextPage(context.Background())
		if err != nil {
			t.Fatalf("paper check: %v", err)
		}
		if ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no paper loaded")
		}
		time.Sleep(300 * time.Millisecond)
	}
	time.Sleep(time.Second)

	start := time.Now()
	page, err := sess.scanSides(context.Background(), driver.ScanOptions{Duplex: duplex, Resolution: res})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	t.Logf("scanned in %s", time.Since(start).Round(100*time.Millisecond))

	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	sides := map[string]*side{"front": &page.Front, "back": page.Back}
	for name, side := range sides {
		if side == nil {
			continue
		}
		path := filepath.Join(out, name+".jpg")
		if err := os.WriteFile(path, side.JPEG, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("%s: %d bytes at %dx%d dpi -> %s", name, len(side.JPEG), side.XDPI, side.YDPI, path)
	}
}
