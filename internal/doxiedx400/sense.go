package doxiedx400

import (
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/andiapps-dev/doxie-scanner/internal/scsiusb"
)

// Conditions the scanner reports, worded for the person at the scanner.
var (
	ErrNoPaper    = errors.New("no paper in the feeder")
	ErrPaperJam   = errors.New("paper jam: open the scanner, remove the sheet, then turn it off and on")
	ErrCoverOpen  = errors.New("the scanner is open: close it and try again")
	ErrMultiFeed  = errors.New("two sheets went through at once: rescan them one at a time")
	ErrSheetStuck = errors.New("a sheet stopped partway through the scanner: open the scanner, take the sheet out, then scan the rest again")
)

var senseErrors = map[byte]error{
	ascqPaperJam:   ErrPaperJam,
	ascqCoverOpen:  ErrCoverOpen,
	ascqChuteEmpty: ErrNoPaper,
	ascqMultiFeed:  ErrMultiFeed,
	ascqSheetStuck: ErrSheetStuck,
}

const (
	senseMinLen    = 31
	offASC         = 7
	offASCQ        = 8
	offValidBytes  = 9
	offSenseLines  = 25
	offSideBytes   = 27
	ascADF         = 0x80
	ascqPaperJam   = 0x01
	ascqCoverOpen  = 0x02
	ascqChuteEmpty = 0x03
	ascqPaperEnd   = 0x04
	ascqMultiFeed  = 0x05
	// Seen when the host was seconds late asking for a sheet the scanner
	// had already started pulling in; the sheet stays where it stopped.
	ascqSheetStuck = 0x09
)

// sense is the vendor sense block (opcode 0xE3). At the end of a side it
// reports how many bytes of the final READ chunk are real, the side's
// line count, and the side's total byte count.
type sense struct {
	asc, ascq  byte
	validBytes int
	lines      int
	sideBytes  int
	raw        []byte
}

func parseSense(b []byte) (sense, error) {
	if len(b) < senseMinLen {
		return sense{}, &scsiusb.ScsiError{Message: fmt.Sprintf("short sense block: %d bytes", len(b))}
	}
	return sense{
		asc:        b[offASC],
		ascq:       b[offASCQ],
		validBytes: int(binary.BigEndian.Uint32(b[offValidBytes:])),
		lines:      int(binary.BigEndian.Uint16(b[offSenseLines:])),
		sideBytes:  int(binary.BigEndian.Uint32(b[offSideBytes:])),
		raw:        b,
	}, nil
}

func (s sense) endOfSide() bool { return s.asc == ascADF && s.ascq == ascqPaperEnd }

func (s sense) err() error {
	if known, ok := senseErrors[s.ascq]; ok && s.asc == ascADF {
		return known
	}
	return &scsiusb.ScsiError{Message: fmt.Sprintf("scanner reported asc=%#02x ascq=%#02x (sense % x)", s.asc, s.ascq, s.raw)}
}
