// Package doxiedx400 implements the driver.Driver interface for the
// Doxie Pro DX400 (Avision OEM hardware, USB VID:PID 2740:000c). It is a
// direct port of the reference Python implementation
// (Utilities/doxie-pro-scanner/doxie_scsi_scan.py in the andiapps-dev
// monorepo history) — a from-scratch reverse-engineering of the standard
// SCSI scanner command set this device accepts over plain USB bulk
// transfers, with no SANE dependency.
package doxiedx400

import "encoding/hex"

// USB identity.
const (
	VID uint16 = 0x2740
	PID uint16 = 0x000c

	// Endpoint addresses as documented by the device; scsiusb.Open wants
	// plain endpoint numbers, so these get masked down to the low nibble
	// when opening the device (see driver.go).
	epBulkIn  = 0x81
	epBulkOut = 0x02

	usbInterfaceNum = 0
)

// Standard SCSI opcodes used by this device's command set.
const (
	opTestUnitReady byte = 0x00
	opRequestSense  byte = 0x03
	opMediaCheck    byte = 0x08
	opInquiry       byte = 0x12
	opReserveUnit   byte = 0x16
	opReleaseUnit   byte = 0x17
	opScan          byte = 0x1b
	opSetWindow     byte = 0x24
	opRead          byte = 0x28
	opSend          byte = 0x2a
)

// Avision "datatype" byte (CDB byte index 2) for READ/SEND commands.
const (
	datatypeReadImageData     byte = 0x00
	datatypeGetCalibrationFmt byte = 0x60
	datatypeDownloadGamma     byte = 0x81
	datatype3x3ColorMatrix    byte = 0x83
	datatypeAttachTruncHead   byte = 0x95
	datatypeAttachTruncTail   byte = 0x96
)

// dataDQ is a device-specific "data type qualifier" observed in the
// original USB capture — used on both the calibration-format READ and
// every image-data READ.
const dataDQ uint16 = 0x0a0d

// mustDecodeHex decodes a hex string that is known-constant at compile
// time; a decode failure here means the literal itself is corrupt, which
// can only happen from a transcription error, so panicking during
// package init is the right failure mode (caught immediately by
// protocol_test.go, long before it could reach real hardware).
func mustDecodeHex(s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		panic("doxiedx400: invalid hex literal: " + err.Error())
	}
	return b
}

// windowData is the 70-byte SET WINDOW payload for a 300 DPI, full-color
// (24-bit) single-sided scan — byte-exact copy of a confirmed-working
// capture. Layout: header(8) = reserved0[6] + desclen=0x3e(62);
// descriptor(42) = winid=0, xres=300, yres=300, ulx=0, uly=0,
// width=10177 (1/1200" units), length=16769, brightness/threshold/
// contrast=128, image_comp=5 (truecolor), bpc=8; avision tail(20) =
// line_width=7632, line_count=4192, ... Copied verbatim from the
// reference Python implementation — never regenerate this by hand.
var windowData = mustDecodeHex(
	"000000000000003e0000012c012c0000000000000000000027c1000041818080" +
		"80050800000300000000000000000000ff14e0ff001dd0106010000000000000" +
		"000000000000",
)

// windowDataDuplex is the same window, but for "ADF Duplex" source mode:
// line_count doubles (0x1060 -> 0x20c0) and the avision bitset3 byte gets
// bit 4 set (0x00 -> 0x10), enabling the ASIC's interlaced-duplex
// front/rear readout. Confirmed by diffing a captured single-sided vs.
// duplex SET WINDOW payload byte-for-byte — every other byte is identical
// to windowData.
var windowDataDuplex = mustDecodeHex(
	"000000000000003e0000012c012c0000000000000000000027c1000041818080" +
		"80050800000300000000000000000000ff14e0ff001dd020c010000000000000" +
		"001000000000",
)

// colorMatrix is the 3x3 color-correction matrix (18 bytes) sent with
// datatype 0x83, captured verbatim.
var colorMatrix = mustDecodeHex(
	"040000000000000004000000000000000400",
)

// gammaTable is the 512-byte gamma curve sent identically for all three
// color channels, captured verbatim.
var gammaTable = mustDecodeHex(
	"000a15181c1f222427292b2d2f303233353638393b3c3d3e4041424344454748" +
		"494a4b4c4d4e4f5051515253545556565758595a5b5b5c5d5e5e5f6061616263" +
		"646465656667686869696a6b6c6c6d6d6e6e6f70717172727373747475767777" +
		"787879797a7a7b7b7c7c7d7d7e7e7f7f80808181828283838484858586868787" +
		"888889898a8a8b8b8c8c8d8d8e8e8f8f90909191929292929393949495959696" +
		"97979898989899999a9a9b9b9c9c9d9d9d9d9e9e9f9fa0a0a1a1a1a1a2a2a3a3" +
		"a4a4a4a4a5a5a6a6a7a7a8a8a8a8a9a9aaaaaaaaababacacadadadadaeaeafaf" +
		"b0b0b0b0b1b1b2b2b2b2b3b3b4b4b4b4b5b5b6b6b6b6b7b7b8b8b8b8b9b9baba" +
		"bababbbbbcbcbcbcbdbdbebebebebfbfc0c0c0c0c1c1c2c2c2c2c3c3c3c3c4c4" +
		"c5c5c5c5c6c6c6c6c7c7c8c8c8c8c9c9c9c9cacacbcbcbcbcccccccccdcdcece" +
		"cececfcfcfcfd0d0d1d1d1d1d2d2d2d2d3d3d3d3d4d4d4d4d5d5d6d6d6d6d7d7" +
		"d7d7d8d8d8d8d9d9d9d9dadadbdbdbdbdcdcdcdcdddddddddededededfdfdfdf" +
		"e0e0e0e0e1e1e1e1e2e2e3e3e3e3e4e4e4e4e5e5e5e5e6e6e6e6e7e7e7e7e8e8" +
		"e8e8e9e9e9e9eaeaeaeaebebebebececececededededeeeeeeeeefefefeff0f0" +
		"f0f0f1f1f1f1f1f1f2f2f2f2f3f3f3f3f4f4f4f4f5f5f5f5f6f6f6f6f7f7f7f7" +
		"f8f8f8f8f9f9f9f9f9f9fafafafafbfbfbfbfcfcfcfcfdfdfdfdfefefefeffff",
)

// Image geometry for this window: 2544px width * 3 channels (RGB).
const (
	lineWidthBytes = 7632
	channels       = 3
	imageWidthPx   = lineWidthBytes / channels
)

// readChunkBytes is the fixed size of every image-data READ, confirmed
// against the real capture: 122112 = 7632 * 16 scanlines. This is also
// the duplex "stripe" size — front/rear alternate one stripe per chunk.
const readChunkBytes = 122112

// senseBufferLen matches scsiusb's standard REQUEST SENSE allocation
// length (22 bytes) — declared here too since sense.go indexes directly
// into a buffer of this size.
const senseBufferLen = 0x16
