package doxiedx400

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/jpeg"
	"testing"
)

// splitJPEG separates a baseline JPEG into its quantization tables
// (natural order, luma then chroma) and the scan data after SOS.
func splitJPEG(t *testing.T, jpg []byte) (quant, scan []byte) {
	t.Helper()
	quant = make([]byte, 2*quantLen)
	for i := 2; i+4 <= len(jpg); {
		m := jpg[i+1]
		end := i + 2 + int(binary.BigEndian.Uint16(jpg[i+2:]))
		if m == 0xDB {
			for p := i + 4; p < end; p += 1 + quantLen {
				table := int(jpg[p] & 0x0f)
				for z, k := range zigzag {
					quant[table*quantLen+k] = jpg[p+1+z]
				}
			}
		}
		if m == markerSOS {
			return quant, jpg[end:]
		}
		i = end
	}
	t.Fatal("no SOS segment")
	return nil, nil
}

// sampling420 is the chroma subsampling of Go's own JPEG encoder, which the
// round-trip test below uses; the scanner itself sends 4:2:2 (sampling422).
const sampling420 byte = 0x22

func TestJFIFHeader_RoundTripsGoEncodedScan(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 64, 48))
	for y := 0; y < 48; y++ {
		for x := 0; x < 64; x++ {
			src.Set(x, y, color.RGBA{uint8(x * 4), uint8(y * 5), uint8((x + y) * 2), 0xff})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, src, &jpeg.Options{Quality: 80}); err != nil {
		t.Fatal(err)
	}
	quant, scan := splitJPEG(t, buf.Bytes())

	rebuilt := append(jfifHeader(64, 48, quant, sampling420), scan...)

	want, err := jpeg.Decode(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	got, err := jpeg.Decode(bytes.NewReader(rebuilt))
	if err != nil {
		t.Fatalf("rebuilt JPEG doesn't decode: %v", err)
	}
	w, g := want.(*image.YCbCr), got.(*image.YCbCr)
	if !bytes.Equal(w.Y, g.Y) || !bytes.Equal(w.Cb, g.Cb) || !bytes.Equal(w.Cr, g.Cr) {
		t.Error("rebuilt JPEG decodes to different pixels: DQT order, DHT or SOS is wrong")
	}
}

func TestWrapJPEG_DecodesScannerStyleFixture(t *testing.T) {
	img, err := jpeg.Decode(bytes.NewReader(wrapJPEG(2550, 32, quantTables[300], fixture(t, "ink-300.scan"))))
	if err != nil {
		t.Fatal(err)
	}
	ycc, ok := img.(*image.YCbCr)
	if !ok || ycc.SubsampleRatio != image.YCbCrSubsampleRatio422 {
		t.Fatalf("want 4:2:2 YCbCr, got %T", img)
	}
	if img.Bounds().Dx() != 2550 || img.Bounds().Dy() != 32 {
		t.Errorf("size %v, want 2550x32", img.Bounds().Size())
	}
}

func TestWrapJPEG_TrimsPaddingAfterEOI(t *testing.T) {
	scan := []byte{0x12, 0x34, 0xFF, 0xD9, 0x00, 0x00, 0xFF, 0xD9}
	out := wrapJPEG(16, 8, quantTables[300], scan)
	if !bytes.HasSuffix(out, []byte{0x12, 0x34, 0xFF, 0xD9}) {
		t.Errorf("tail % x: padding after the first EOI should be dropped", out[len(out)-8:])
	}
}

func TestWrapJPEG_AddsMissingEOI(t *testing.T) {
	out := wrapJPEG(16, 8, quantTables[300], []byte{0x12, 0x34})
	if !bytes.HasSuffix(out, []byte{0x12, 0x34, 0xFF, 0xD9}) {
		t.Errorf("tail % x: want EOI appended", out[len(out)-4:])
	}
}

func TestStandardDHTHasAllFourTables(t *testing.T) {
	if !bytes.HasPrefix(standardDHT, []byte{0xFF, markerDHT}) {
		t.Fatalf("standardDHT starts % x", standardDHT[:2])
	}
	// Annex K: 2 DC tables of 1+16+12 bytes and 2 AC tables of 1+16+162.
	if want := 4 + 2*(1+16+12) + 2*(1+16+162); len(standardDHT) != want {
		t.Errorf("standardDHT is %d bytes, want %d", len(standardDHT), want)
	}
}

func TestSegments_StopsAtTruncatedSegment(t *testing.T) {
	jpg := []byte{0xFF, 0xD8, 0xFF, markerDHT, 0x00, 0x40, 0x00}
	if got := segments(jpg, markerDHT); got != nil {
		t.Errorf("a segment running past the end should be ignored, got % x", got)
	}
}
