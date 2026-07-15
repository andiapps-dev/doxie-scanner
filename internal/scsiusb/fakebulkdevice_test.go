package scsiusb

import (
	"bytes"
	"errors"
	"testing"
)

// fakeRead scripts a single response to one Read() call.
type fakeRead struct {
	data []byte
	err  error
}

// fakeBulkDevice is an in-memory BulkDevice used to exercise Command()'s
// write/read/status/retry state machine without any real USB hardware.
// Writes and reads are each consumed from their own ordered queue, since
// Command() always writes before it reads within a single exchange.
type fakeBulkDevice struct {
	t *testing.T

	wantWrites [][]byte // if non-nil, the next Write() must match exactly
	writeLog   [][]byte

	reads []fakeRead

	closed bool
}

func (f *fakeBulkDevice) Write(p []byte) (int, error) {
	cp := append([]byte(nil), p...)
	f.writeLog = append(f.writeLog, cp)
	if len(f.wantWrites) > 0 {
		want := f.wantWrites[0]
		f.wantWrites = f.wantWrites[1:]
		if want != nil && !bytes.Equal(want, cp) {
			f.t.Errorf("unexpected write: got % x, want % x", cp, want)
		}
	}
	return len(p), nil
}

func (f *fakeBulkDevice) Read(p []byte) (int, error) {
	if len(f.reads) == 0 {
		return 0, errors.New("fakeBulkDevice: no more scripted reads")
	}
	r := f.reads[0]
	f.reads = f.reads[1:]
	if r.err != nil {
		return 0, r.err
	}
	n := copy(p, r.data)
	return n, nil
}

func (f *fakeBulkDevice) Close() error {
	f.closed = true
	return nil
}

// queueRead appends a scripted successful read response.
func (f *fakeBulkDevice) queueRead(data []byte) {
	f.reads = append(f.reads, fakeRead{data: data})
}

// queueReadErr appends a scripted read failure.
func (f *fakeBulkDevice) queueReadErr(err error) {
	f.reads = append(f.reads, fakeRead{err: err})
}

// queueGoodStatus appends the single-byte "status good" response.
func (f *fakeBulkDevice) queueGoodStatus() {
	f.queueRead([]byte{StatusGood})
}
