package doxiedx400

import (
	"errors"
	"time"

	"github.com/andiapps-dev/doxie-scanner/internal/scsiusb"
)

const (
	defaultRetries    = 4
	defaultRetryDelay = 200 * time.Millisecond
)

// command runs one exchange and hands back any check condition as a
// parsed sense block instead of an error, since a check condition is how
// the scanner signals the normal end of a side.
func command(dev scsiusb.BulkDevice, cdb, out []byte, inLen, retries int) ([]byte, *sense, error) {
	data, err := scsiusb.Command12(dev, cdb, out, inLen, retries, defaultRetryDelay)
	if err == nil {
		return data, nil, nil
	}
	var cc *scsiusb.CheckConditionError
	if !errors.As(err, &cc) {
		return nil, nil, err
	}
	s, perr := parseSense(cc.Sense)
	if perr != nil {
		return nil, nil, perr
	}
	return data, &s, nil
}

func mustSucceed(dev scsiusb.BulkDevice, cdb, out []byte, inLen int) ([]byte, error) {
	data, s, err := command(dev, cdb, out, inLen, defaultRetries)
	if err != nil {
		return nil, err
	}
	if s != nil {
		return nil, s.err()
	}
	return data, nil
}

func send(dev scsiusb.BulkDevice, dataType byte, payload []byte) error {
	_, err := mustSucceed(dev, scsiusb.CDB12(opSend, dataType, len(payload), 0), payload, 0)
	return err
}

func read(dev scsiusb.BulkDevice, dataType byte, n int) ([]byte, error) {
	return mustSucceed(dev, scsiusb.CDB12(opRead, dataType, n, 0), nil, n)
}

func control(dev scsiusb.BulkDevice, op, flags byte) error {
	_, err := mustSucceed(dev, scsiusb.CDB12(op, 0, 0, flags), nil, 0)
	return err
}

func hasPaper(dev scsiusb.BulkDevice) (bool, error) {
	data, err := mustSucceed(dev, scsiusb.CDB12(opSensor, 0, sensorReplyLn, 0), nil, sensorReplyLn)
	if err != nil {
		return false, err
	}
	return len(data) == sensorReplyLn && data[4]&0x01 != 0, nil
}

func setWindow(dev scsiusb.BulkDevice, w window) error {
	payload := w.bytes()
	_, err := mustSucceed(dev, scsiusb.CDB12(opSetWindow, 0, len(payload), 0), payload, 0)
	return err
}

// readImageChunk is never retried: re-sending a READ would skip a chunk.
func readImageChunk(dev scsiusb.BulkDevice) ([]byte, *sense, error) {
	return command(dev, scsiusb.CDB12(opRead, dtImageData, readChunkBytes, 0), nil, readChunkBytes, 1)
}
