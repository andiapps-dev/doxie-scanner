package doxiedx400

import (
	"encoding/binary"
	"fmt"
)

// The 96-byte SET WINDOW (opcode 0xE4) payload the vendor driver sends,
// captured from Doxie.app 3.5.1 for a 300 dpi duplex color JPEG scan of a
// full-width, 14"-max page. Only the fields patched in buildWindow vary
// across the captured 200/300/600 dpi simplex/duplex scans.
var windowTemplate = mustDecodeHex(
	"0000020060012c0000000000000000000009f600001068012c012c00000a0000" +
		"00106800001e000000008000040302840000808000ff00208000000100000005" +
		"0000000000000000000000000000000000000000000000000000000000000000",
)

const (
	windowLen = 0x60

	offXRes       = 23
	offYRes       = 25
	offPixels     = 27
	offLines      = 31
	offBytesLine  = 35
	offScanMethod = 44

	scanMethodSimplex = 0x01
	scanMethodDuplex  = 0x04

	baseRes        = 300
	maxWidthAtBase = 2550
	maxLenAtBase   = 4200
	bytesPerPixel  = 3
	pixelAlign     = 16
)

// SupportedResolutions are the vertical resolutions the scanner is driven
// at. The sensor only reads 300 or 600 dpi across the page, so 200 dpi is
// scanned at 300 across and must be downscaled horizontally afterwards.
var SupportedResolutions = []int{200, 300, 600}

type window struct {
	xRes, yRes   int
	width        int
	pixels       int
	lines        int
	bytesPerLine int
	duplex       bool
}

func newWindow(res int, duplex bool) (window, error) {
	var xRes int
	switch res {
	case 200, 300:
		xRes = 300
	case 600:
		xRes = 600
	default:
		return window{}, fmt.Errorf("unsupported resolution %d dpi (want one of %v)", res, SupportedResolutions)
	}
	width := maxWidthAtBase * xRes / baseRes
	pixels := alignUp(width, pixelAlign)
	return window{
		xRes:         xRes,
		yRes:         res,
		width:        width,
		pixels:       pixels,
		lines:        maxLenAtBase * res / baseRes,
		bytesPerLine: pixels * bytesPerPixel,
		duplex:       duplex,
	}, nil
}

func (w window) bytes() []byte {
	b := append([]byte(nil), windowTemplate...)
	binary.BigEndian.PutUint16(b[offXRes:], uint16(w.xRes))
	binary.BigEndian.PutUint16(b[offYRes:], uint16(w.yRes))
	binary.BigEndian.PutUint32(b[offPixels:], uint32(w.pixels))
	binary.BigEndian.PutUint32(b[offLines:], uint32(w.lines))
	binary.BigEndian.PutUint32(b[offBytesLine:], uint32(w.bytesPerLine))
	b[offScanMethod] = scanMethodSimplex
	if w.duplex {
		b[offScanMethod] = scanMethodDuplex
	}
	return b
}

func alignUp(n, align int) int {
	return (n + align - 1) / align * align
}
