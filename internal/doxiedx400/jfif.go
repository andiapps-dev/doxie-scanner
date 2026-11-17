package doxiedx400

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/jpeg"
)

// The scanner sends bare baseline-JPEG scan data (no SOI, tables or
// frame header) ending in EOI. The host supplies the header: the
// quantization tables it uploaded, 4:2:2 chroma, and the standard
// Huffman tables from the JPEG spec's Annex K.

const (
	sampling422 byte = 0x21
	markerDHT   byte = 0xC4
	markerSOS   byte = 0xDA
	quantLen         = 64
)

var eoi = []byte{0xFF, 0xD9}

var zigzag = [quantLen]int{
	0, 1, 8, 16, 9, 2, 3, 10, 17, 24, 32, 25, 18, 11, 4, 5,
	12, 19, 26, 33, 40, 48, 41, 34, 27, 20, 13, 6, 7, 14, 21, 28,
	35, 42, 49, 56, 57, 50, 43, 36, 29, 22, 15, 23, 30, 37, 44, 51,
	58, 59, 52, 45, 38, 31, 39, 46, 53, 60, 61, 54, 47, 55, 62, 63,
}

// Go's encoder always writes the four Annex K tables, so lifting its DHT
// segment avoids transcribing 400-odd table bytes by hand.
var standardDHT = mustStandardDHT()

func mustStandardDHT() []byte {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 16, 16)), nil); err != nil {
		panic("doxiedx400: encode probe JPEG: " + err.Error())
	}
	dht := segments(buf.Bytes(), markerDHT)
	if len(dht) == 0 {
		panic("doxiedx400: probe JPEG has no DHT segment")
	}
	return dht
}

// segments returns the raw bytes of every header segment with the given
// marker, stopping at the start of scan.
func segments(jpg []byte, marker byte) []byte {
	var out []byte
	for i := 2; i+4 <= len(jpg) && jpg[i] == 0xFF; {
		m := jpg[i+1]
		end := i + 2 + int(binary.BigEndian.Uint16(jpg[i+2:]))
		if end > len(jpg) {
			break
		}
		if m == marker {
			out = append(out, jpg[i:end]...)
		}
		if m == markerSOS {
			break
		}
		i = end
	}
	return out
}

func writeSegment(b *bytes.Buffer, marker byte, payload []byte) {
	b.Write([]byte{0xFF, marker, byte((len(payload) + 2) >> 8), byte(len(payload) + 2)})
	b.Write(payload)
}

// jfifHeader builds SOI through SOS. quant holds the luma then chroma
// tables in natural order, exactly as uploaded to the scanner.
func jfifHeader(width, height int, quant []byte, ySampling byte) []byte {
	var b bytes.Buffer
	b.Write([]byte{0xFF, 0xD8})

	dqt := make([]byte, 0, 2*(quantLen+1))
	for table := 0; table < 2; table++ {
		dqt = append(dqt, byte(table))
		for _, k := range zigzag {
			dqt = append(dqt, quant[table*quantLen+k])
		}
	}
	writeSegment(&b, 0xDB, dqt)

	writeSegment(&b, 0xC0, []byte{
		8, byte(height >> 8), byte(height), byte(width >> 8), byte(width), 3,
		1, ySampling, 0,
		2, 0x11, 1,
		3, 0x11, 1,
	})
	b.Write(standardDHT)
	writeSegment(&b, markerSOS, []byte{3, 1, 0x00, 2, 0x11, 3, 0x11, 0, 63, 0})
	return b.Bytes()
}

// wrapJPEG turns one side's scan data into a complete JPEG file. The
// first FF D9 is the real end of image (entropy-coded data byte-stuffs
// every 0xFF), so anything after it is padding.
func wrapJPEG(width, height int, quant, scan []byte) []byte {
	if i := bytes.Index(scan, eoi); i >= 0 {
		scan = scan[:i+len(eoi)]
	}
	out := append(jfifHeader(width, height, quant, sampling422), scan...)
	if !bytes.HasSuffix(out, eoi) {
		out = append(out, eoi...)
	}
	return out
}
