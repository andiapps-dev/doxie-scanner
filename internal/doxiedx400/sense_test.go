package doxiedx400

import (
	"encoding/hex"
	"errors"
	"testing"

	"github.com/andiapps-dev/doxie-scanner/internal/scsiusb"
)

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParseSense_CapturedEndOfSide(t *testing.T) {
	b := mustHex(t, "0000022cf0032580040002234000000000000000000a0000000ce8001a2340000000001e0000000080000000")
	s, err := parseSense(b)
	if err != nil {
		t.Fatal(err)
	}
	if !s.endOfSide() {
		t.Error("expected end of side")
	}
	if s.validBytes != 0x22340 || s.lines != 3304 || s.sideBytes != 0x1a2340 {
		t.Errorf("got valid=%d lines=%d side=%d", s.validBytes, s.lines, s.sideBytes)
	}
}

func TestParseSense_ChuteEmptyIsNoPaper(t *testing.T) {
	b := mustHex(t, "0000022cf00325800300000056c000000000000000000013f0000019b0007056c0000000003bd000000080000000")
	s, err := parseSense(b)
	if err != nil {
		t.Fatal(err)
	}
	if s.endOfSide() {
		t.Error("chute empty is not end of side")
	}
	if !errors.Is(s.err(), ErrNoPaper) {
		t.Errorf("got %v, want ErrNoPaper", s.err())
	}
}

func TestSenseErr_UnknownCodeIsScsiError(t *testing.T) {
	s, _ := parseSense(senseBlock(0x3a, 0x00, 0, 0, 0))
	var se *scsiusb.ScsiError
	if !errors.As(s.err(), &se) {
		t.Errorf("got %T, want *scsiusb.ScsiError", s.err())
	}
}

func TestParseSense_RejectsShortBlock(t *testing.T) {
	if _, err := parseSense(make([]byte, senseMinLen-1)); err == nil {
		t.Fatal("expected an error")
	}
}

func TestSenseErr_KnownConditionsReadPlainly(t *testing.T) {
	for ascq, want := range map[byte]error{0x01: ErrPaperJam, 0x02: ErrCoverOpen, 0x03: ErrNoPaper, 0x05: ErrMultiFeed, 0x09: ErrSheetStuck} {
		s, _ := parseSense(senseBlock(ascADF, ascq, 0, 0, 0))
		if !errors.Is(s.err(), want) {
			t.Errorf("ascq %#02x: got %v, want %v", ascq, s.err(), want)
		}
	}
}
