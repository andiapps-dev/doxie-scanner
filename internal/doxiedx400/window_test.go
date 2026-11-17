package doxiedx400

import (
	"bytes"
	"encoding/hex"
	"strings"
	"testing"
)

// Window payloads captured from Doxie.app's driver debug log
// (/tmp/DrvLog/AVDrv.txt) on real DX400 hardware.
var capturedWindows = map[string]string{
	"300 duplex":  "00 00 02 00 60 01 2c 00 00 00 00 00 00 00 00 00 00 09 f6 00 00 10 68 01 2c 01 2c 00 00 0a 00 00 00 10 68 00 00 1e 00 00 00 00 80 00 04 03 02 84 00 00 80 80 00 ff 00 20 80 00 00 01 00 00 00 05",
	"200 duplex":  "00 00 02 00 60 01 2c 00 00 00 00 00 00 00 00 00 00 09 f6 00 00 10 68 01 2c 00 c8 00 00 0a 00 00 00 0a f0 00 00 1e 00 00 00 00 80 00 04 03 02 84 00 00 80 80 00 ff 00 20 80 00 00 01 00 00 00 05",
	"300 simplex": "00 00 02 00 60 01 2c 00 00 00 00 00 00 00 00 00 00 09 f6 00 00 10 68 01 2c 01 2c 00 00 0a 00 00 00 10 68 00 00 1e 00 00 00 00 80 00 01 03 02 84 00 00 80 80 00 ff 00 20 80 00 00 01 00 00 00 05",
	"600 duplex":  "00 00 02 00 60 01 2c 00 00 00 00 00 00 00 00 00 00 09 f6 00 00 10 68 02 58 02 58 00 00 13 f0 00 00 20 d0 00 00 3b d0 00 00 00 80 00 04 03 02 84 00 00 80 80 00 ff 00 20 80 00 00 01 00 00 00 05",
}

func capturedWindow(t *testing.T, name string) []byte {
	t.Helper()
	b, err := hex.DecodeString(strings.ReplaceAll(capturedWindows[name], " ", ""))
	if err != nil {
		t.Fatalf("bad capture %q: %v", name, err)
	}
	return append(b, make([]byte, windowLen-len(b))...)
}

func TestWindowMatchesCaptures(t *testing.T) {
	cases := []struct {
		name   string
		res    int
		duplex bool
	}{
		{"300 duplex", 300, true},
		{"200 duplex", 200, true},
		{"300 simplex", 300, false},
		{"600 duplex", 600, true},
	}
	for _, c := range cases {
		w, err := newWindow(c.res, c.duplex)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		got, want := w.bytes(), capturedWindow(t, c.name)
		if !bytes.Equal(got, want) {
			t.Errorf("%s:\n got  % x\n want % x", c.name, got, want)
		}
	}
}

func TestWindowGeometry(t *testing.T) {
	w, _ := newWindow(600, false)
	if w.pixels != 5104 || w.lines != 8400 || w.bytesPerLine != 15312 {
		t.Errorf("600 dpi geometry: %+v", w)
	}
	w, _ = newWindow(200, true)
	if w.xRes != 300 || w.yRes != 200 {
		t.Errorf("200 dpi must scan at 300 across: %+v", w)
	}
}

func TestWindowRejectsUnsupportedResolution(t *testing.T) {
	for _, res := range []int{0, 150, 400, 1200} {
		if _, err := newWindow(res, true); err == nil {
			t.Errorf("expected %d dpi to be rejected", res)
		}
	}
}

func TestWindowTemplateLength(t *testing.T) {
	if len(windowTemplate) != windowLen {
		t.Fatalf("template is %d bytes, want %d", len(windowTemplate), windowLen)
	}
}
