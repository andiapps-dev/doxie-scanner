package doxiedx400

import "testing"

// These tests exist specifically to catch byte-literal transcription
// errors in protocol.go's hex constants — historically the single most
// common bug in the reference Python implementation. They should be the
// first thing run against any change to those literals, well before ever
// touching real hardware.

func TestWindowDataLengths(t *testing.T) {
	if len(windowData) != 70 {
		t.Errorf("windowData: got %d bytes, want 70", len(windowData))
	}
	if len(windowDataDuplex) != 70 {
		t.Errorf("windowDataDuplex: got %d bytes, want 70", len(windowDataDuplex))
	}
}

func TestColorMatrixLength(t *testing.T) {
	if len(colorMatrix) != 18 {
		t.Errorf("colorMatrix: got %d bytes, want 18", len(colorMatrix))
	}
}

func TestGammaTableLength(t *testing.T) {
	if len(gammaTable) != 512 {
		t.Errorf("gammaTable: got %d bytes, want 512", len(gammaTable))
	}
}

// TestWindowDataDuplexDiff asserts windowData and windowDataDuplex differ
// in exactly the two bytes documented in protocol.go: the doubled
// line_count field (offsets 55-56) and the avision bitset3 byte (offset
// 65) gaining bit 4. Any other diff means one of the literals was
// mistranscribed.
func TestWindowDataDuplexDiff(t *testing.T) {
	if len(windowData) != len(windowDataDuplex) {
		t.Fatalf("length mismatch: %d vs %d", len(windowData), len(windowDataDuplex))
	}

	var diffOffsets []int
	for i := range windowData {
		if windowData[i] != windowDataDuplex[i] {
			diffOffsets = append(diffOffsets, i)
		}
	}

	wantOffsets := []int{55, 56, 65}
	if len(diffOffsets) != len(wantOffsets) {
		t.Fatalf("expected diffs at offsets %v, got %v", wantOffsets, diffOffsets)
	}
	for i, off := range diffOffsets {
		if off != wantOffsets[i] {
			t.Errorf("diff offset %d: got %d, want %d", i, off, wantOffsets[i])
		}
	}

	// line_count: 0x1060 -> 0x20c0 (big-endian 16-bit at offset 55-56).
	single := int(windowData[55])<<8 | int(windowData[56])
	duplex := int(windowDataDuplex[55])<<8 | int(windowDataDuplex[56])
	if single != 0x1060 {
		t.Errorf("windowData line_count: got %#04x, want 0x1060", single)
	}
	if duplex != 0x20c0 {
		t.Errorf("windowDataDuplex line_count: got %#04x, want 0x20c0", duplex)
	}
	if duplex != single*2 {
		t.Errorf("duplex line_count should be exactly double: %#04x vs %#04x", duplex, single)
	}

	// avision bitset3: 0x00 -> 0x10 (bit 4 set).
	if windowData[65] != 0x00 {
		t.Errorf("windowData bitset3: got %#02x, want 0x00", windowData[65])
	}
	if windowDataDuplex[65] != 0x10 {
		t.Errorf("windowDataDuplex bitset3: got %#02x, want 0x10", windowDataDuplex[65])
	}
}

func TestReadChunkBytesMatchesLineWidthTimes16(t *testing.T) {
	if readChunkBytes != lineWidthBytes*16 {
		t.Errorf("readChunkBytes (%d) should equal lineWidthBytes*16 (%d)", readChunkBytes, lineWidthBytes*16)
	}
}

func TestImageWidthPx(t *testing.T) {
	if imageWidthPx != 2544 {
		t.Errorf("imageWidthPx: got %d, want 2544", imageWidthPx)
	}
}

func TestMustDecodeHex_PanicsOnInvalidHex(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected a panic for invalid hex")
		}
	}()
	mustDecodeHex("not-hex")
}
