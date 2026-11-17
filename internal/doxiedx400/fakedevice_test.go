package doxiedx400

import (
	"encoding/binary"
	"errors"
	"os"
	"testing"
)

// fakeDevice scripts a DX400: CDBs arrive through SendCommand (like the
// real vendor control request), payloads through Write, and every Read
// pops the next scripted response.
type fakeDevice struct {
	t        *testing.T
	commands [][]byte
	payloads [][]byte
	reads    [][]byte
	readErr  error
	closeErr error
	closed   bool
}

func (f *fakeDevice) SendCommand(cdb []byte) error {
	f.commands = append(f.commands, append([]byte(nil), cdb...))
	return nil
}

func (f *fakeDevice) Write(p []byte) (int, error) {
	f.payloads = append(f.payloads, append([]byte(nil), p...))
	return len(p), nil
}

func (f *fakeDevice) Read(p []byte) (int, error) {
	if len(f.reads) == 0 {
		if f.readErr != nil {
			return 0, f.readErr
		}
		return 0, errors.New("fakeDevice: no more scripted reads")
	}
	r := f.reads[0]
	f.reads = f.reads[1:]
	return copy(p, r), nil
}

func (f *fakeDevice) Close() error {
	f.closed = true
	return f.closeErr
}

var (
	statusGood  = []byte{0x00, 0x00, 0x00}
	statusCheck = []byte{0x00, 0x00, 0x02}
)

func (f *fakeDevice) ok(data []byte) {
	if data != nil {
		f.reads = append(f.reads, data)
	}
	f.reads = append(f.reads, statusGood)
}

func (f *fakeDevice) checkCondition(data, senseBlock []byte) {
	if data != nil {
		f.reads = append(f.reads, data)
	}
	f.reads = append(f.reads, statusCheck, senseBlock, statusGood)
}

// opcodes summarizes the commands sent as "op/datatype/flags" bytes.
func (f *fakeDevice) opcodes() [][3]byte {
	out := make([][3]byte, len(f.commands))
	for i, c := range f.commands {
		out[i] = [3]byte{c[0], c[4], c[11]}
	}
	return out
}

func senseBlock(asc, ascq byte, valid, lines, sideBytes int) []byte {
	b := make([]byte, 0x2C)
	copy(b, []byte{0x00, 0x00, 0x02, 0x2c, 0xf0, 0x03, 0x25})
	b[offASC], b[offASCQ] = asc, ascq
	binary.BigEndian.PutUint32(b[offValidBytes:], uint32(valid))
	binary.BigEndian.PutUint16(b[offSenseLines:], uint16(lines))
	binary.BigEndian.PutUint32(b[offSideBytes:], uint32(sideBytes))
	return b
}

func endOfSide(data []byte, lines int) []byte {
	return senseBlock(ascADF, ascqPaperEnd, len(data), lines, len(data))
}

// scriptedSide is one side's scan data as the scanner would send it,
// delivered in a single final chunk.
type scriptedSide struct {
	data  []byte
	lines int
}

// scriptFirstSheet queues the first sheet of a stack, whose sides each
// fit in a single chunk: the job reserve and full setup, then the sheet.
func (f *fakeDevice) scriptFirstSheet(duplex bool, sides ...scriptedSide) {
	f.ok(nil)             // E6 job
	f.ok(make([]byte, 5)) // A8 A0
	f.ok(nil)             // E4 window
	if duplex {
		f.ok(make([]byte, 7)) // A8 B2
	}
	f.ok(nil) // AA 95
	f.ok(nil) // AA 96
	f.ok(nil) // AA 8B
	f.ok(make([]byte, 0x24))
	f.ok(nil) // gamma 0
	f.ok(nil) // gamma 1
	f.ok(nil) // gamma 2
	f.scriptSides(sides...)
}

// scriptNextSheet queues a later sheet of the same stack: the shorter
// setup, then the sheet.
func (f *fakeDevice) scriptNextSheet(sides ...scriptedSide) {
	f.ok(nil)                // E4 window
	f.ok(nil)                // AA 95
	f.ok(nil)                // AA 96
	f.ok(make([]byte, 0x24)) // A8 60
	f.scriptSides(sides...)
}

func (f *fakeDevice) scriptSides(sides ...scriptedSide) {
	f.ok(nil) // E6 scan unit
	for _, s := range sides {
		f.checkCondition(s.data, endOfSide(s.data, s.lines))
	}
	f.ok(nil) // E7 scan unit
}

// paper queues the reply to one paper check.
func (f *fakeDevice) paper(present bool) {
	reply := []byte{0x00, 0x00, 0x02, 0x05, 0x00}
	if present {
		reply[4] = 0x01
	}
	f.ok(reply)
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
