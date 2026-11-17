// Package doxiedx400 is the driver for the Doxie Pro DX400 (Avision OEM
// hardware, USB VID:PID 2740:000c). It speaks the protocol Doxie's own
// driver uses: a 12-byte command set in which the scanner JPEG-encodes each
// side on the device, with one scan job held open across a whole stack of
// sheets.
//
// The command sequence, the SET WINDOW payloads and the other constants
// here were captured from Doxie's own Windows and macOS drivers talking to
// real hardware, and the driver was written against that wire data in
// github.com/jasonlearst/doxie-scanner (MIT), a fork of this project. This
// package is adapted from that one, as is the test data under testdata/.
// It replaced this project's first driver, which is in the git history.
//
// Open returns a driver.Session. Its ScanPage decodes each side and drops a
// blank duplex back; scanSides, one layer down, hands back the scanner's
// own JPEGs.
package doxiedx400

import "encoding/hex"

const (
	VID uint16 = 0x2740
	PID uint16 = 0x000c

	epBulkIn    = 0x81
	epBulkOut   = 0x02
	epInterrupt = 0x83

	buttonPacketLen      = 8
	buttonPressed   byte = 0x80

	usbInterfaceNum = 0
)

const (
	opSense       byte = 0xE3
	opSetWindow   byte = 0xE4
	opReserve     byte = 0xE6
	opRelease     byte = 0xE7
	opSensor      byte = 0xE8
	opRead        byte = 0xA8
	opSend        byte = 0xAA
	flagJob       byte = 0x02
	flagScanUnit  byte = 0x00
	sensorReplyLn      = 5
)

const (
	dtImageData       byte = 0x00
	dtCalibrationFmt  byte = 0x60
	dtGamma           byte = 0x81
	dtJPEGQuant       byte = 0x8B
	dtTruncateHead    byte = 0x95
	dtTruncateTail    byte = 0x96
	dtScanStatus      byte = 0xA0
	dtDuplexTransfer  byte = 0xB2
	calibrationFmtLen      = 0x24
	scanStatusLen          = 5
	duplexTransferLen      = 7
)

// readChunkBytes is the fixed size of every image-data READ. The scanner
// always returns a full chunk; on a side's last chunk, the sense data
// says how many of those bytes are real.
const readChunkBytes = 0x40000

// Every payload the host sends starts with a 4-byte header:
// 00 00 <version> <total length>.
var truncateNone = mustDecodeHex("0000010a000000000000")

func gammaPayload(channel byte) []byte {
	b := make([]byte, 0, 8+len(gammaCurve))
	b = append(b, 0x00, 0x00, 0x01, 0x02, 0x08, 0x00, channel, 0x08)
	return append(b, gammaCurve...)
}

func quantPayload(tables []byte) []byte {
	return append([]byte{0x00, 0x00, 0x01, 0x84}, tables...)
}

// gammaCurve is the 256-entry, 16-bit-per-entry tone curve Doxie's
// driver sends for every channel at every resolution.
var gammaCurve = mustDecodeHex(
	"0000000c001100160019001d0020002300250028002a002c002f003100330035" +
		"00370039003a003c003e0040004100430045004600480049004b004c004e004f" +
		"0050005200530055005600570059005a005b005c005e005f0060006100620064" +
		"00650066006700680069006b006c006d006e006f007000710072007300740075" +
		"0076007700780079007a007b007c007d007e007f008000810082008300840085" +
		"0086008700880089008a008b008b008c008d008e008f00900091009200920093" +
		"0094009500960097009800980099009a009b009c009d009d009e009f00a000a1" +
		"00a100a200a300a400a500a500a600a700a800a900a900aa00ab00ac00ac00ad" +
		"00ae00af00af00b000b100b200b200b300b400b500b500b600b700b700b800b9" +
		"00ba00ba00bb00bc00bc00bd00be00bf00bf00c000c100c100c200c300c300c4" +
		"00c500c600c600c700c800c800c900ca00ca00cb00cc00cc00cd00ce00ce00cf" +
		"00d000d000d100d100d200d300d300d400d500d500d600d700d700d800d900d9" +
		"00da00da00db00dc00dc00dd00de00de00df00df00e000e100e100e200e200e3" +
		"00e400e400e500e600e600e700e700e800e900e900ea00ea00eb00ec00ec00ed" +
		"00ed00ee00ee00ef00f000f000f100f100f200f300f300f400f400f500f500f6" +
		"00f700f700f800f800f900f900fa00fb00fb00fc00fc00fd00fd00fe00fe00ff",
)

// quantTables are the luma+chroma JPEG quantization tables (natural,
// row-major order) Doxie's driver uploads per resolution. Any tables
// work, since the host writes the same tables into the JPEG header;
// these keep file sizes close to what Doxie's app produces.
var quantTables = map[int][]byte{
	200: mustDecodeHex(
		"04040404040506070404040404070707040404040507080704040404060a0a07" +
			"04040507080d0c09040407080a0c0d0b0608090a0c0e0e0c090b0b0c0d0c0c0c" +
			"040404060c0c0c0c040404080c0c0c0c0404070c0c0c0c0c06080c0c0c0c0c0c" +
			"0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c",
	),
	300: mustDecodeHex(
		"040404040407090b04040404050a0b0a04040404070a0c0a04040405090f0e0b" +
			"0404070a0c13120e04060a0b0e121410090b0e0f121515120d10111114111211" +
			"04040408111111110404050c1111111104050a1111111111080c111111111111" +
			"1111111111111111111111111111111111111111111111111111111111111111",
	),
	600: mustDecodeHex(
		"04040404060a0d1004040405070f100e040404060a0f120e040406080d171510" +
			"05060a0e121c1b1406090e11151b1d180d1114171b1f1f1a131819191d1a1b1a" +
			"0405060c1a1a1a1a050507111a1a1a1a06070e1a1a1a1a1a0c111a1a1a1a1a1a" +
			"1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a",
	),
}

func mustDecodeHex(s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		panic("doxiedx400: invalid hex literal: " + err.Error())
	}
	return b
}
