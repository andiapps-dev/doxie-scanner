package doxiedx400

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

// fakeDevice is an in-memory scsiusb.BulkDevice used to drive
// doxiedx400's command/scan logic without any real USB hardware. Writes
// and reads are each consumed from their own ordered queue, matching how
// scsiusb.Command always writes a CDB (and optional payload) before it
// reads any response.
type fakeDevice struct {
	t *testing.T

	wantWrites [][]byte // if non-nil, the next Write() must match exactly
	writeLog   [][]byte

	reads []fakeRead

	closeErr error
	closed   bool
}

func (f *fakeDevice) Write(p []byte) (int, error) {
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

func (f *fakeDevice) Read(p []byte) (int, error) {
	if len(f.reads) == 0 {
		return 0, errors.New("fakeDevice: no more scripted reads")
	}
	r := f.reads[0]
	f.reads = f.reads[1:]
	if r.err != nil {
		return 0, r.err
	}
	n := copy(p, r.data)
	return n, nil
}

func (f *fakeDevice) Close() error {
	f.closed = true
	return f.closeErr
}

func (f *fakeDevice) queueRead(data []byte) { f.reads = append(f.reads, fakeRead{data: data}) }

func (f *fakeDevice) queueGoodStatus() { f.queueRead([]byte{0x00}) }

// queueOKCommand scripts the reads for one full "successful command"
// exchange: optional inLen bytes of data, followed by a good status
// byte.
func (f *fakeDevice) queueOKCommand(inData []byte) {
	f.queueRead(inData)
	f.queueGoodStatus()
}

// queueCheckConditionNoData scripts a check-condition status (for a
// command with inLen == 0, so there's no preceding data-phase read),
// followed by the REQUEST SENSE exchange (sense buffer with the given
// sense_key/ASC/ASCQ, then the trailing ignored status byte requestSense
// always reads).
func (f *fakeDevice) queueCheckConditionNoData(senseKey, asc, ascq byte) {
	f.queueRead([]byte{0x02}) // StatusCheckCondition
	sense := make([]byte, senseBufferLen)
	sense[2] = senseKey
	sense[12], sense[13] = asc, ascq
	f.queueRead(sense)
	f.queueGoodStatus() // trailing ignored status after REQUEST SENSE
}

// queueEndOfDocumentImageRead scripts a full final image-READ exchange
// that ends the document: since this command has inLen > 0, Command()
// reads the (empty) data phase *before* the status byte — matching the
// real device (and the reference Python implementation) exactly, where
// the last image READ returns zero data bytes and only then reports a
// check condition. Classified as ASC/ASCQ 0x80,0x04 "ADF paper end".
func (f *fakeDevice) queueEndOfDocumentImageRead() {
	f.queueRead(nil) // data phase: 0 bytes, no more image data
	f.queueCheckConditionNoData(0x05, 0x80, 0x04)
}
