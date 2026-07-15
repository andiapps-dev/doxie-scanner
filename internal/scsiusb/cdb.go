package scsiusb

import (
	"errors"
	"time"
)

// MinCDBLen is the minimum CDB length these USB scanners require; shorter
// CDBs are zero-padded up to this length.
const MinCDBLen = 10

// StatusGood, StatusCheckCondition, and StatusBusy are the one-byte status
// values these scanners send after every command completes.
const (
	StatusGood           byte = 0x00
	StatusCheckCondition byte = 0x02
	StatusBusy           byte = 0x08
)

// requestSenseCDB is the standard SCSI REQUEST SENSE command: opcode
// 0x03, allocation length 0x16 (22 bytes). This is standard SCSI, not
// specific to any scanner model, so it lives here rather than in a
// device-specific package.
var requestSenseCDB = PadCDB([]byte{0x03, 0x00, 0x00, 0x00, 0x16})

const senseBufferLen = 0x16

// ErrTimeout is returned by a BulkDevice.Read implementation to indicate
// the read timed out with no data available. readExact treats this as
// "no more data pending" rather than a hard failure, matching the timeout
// tolerance in the original Python implementation.
var ErrTimeout = errors.New("scsiusb: read timed out")

// PadCDB zero-pads cdb up to MinCDBLen bytes. If cdb is already at least
// that long, it's returned unchanged.
func PadCDB(cdb []byte) []byte {
	if len(cdb) >= MinCDBLen {
		return cdb
	}
	padded := make([]byte, MinCDBLen)
	copy(padded, cdb)
	return padded
}

func readExact(dev BulkDevice, length int) ([]byte, error) {
	buf := make([]byte, 0, length)
	for len(buf) < length {
		remaining := length - len(buf)
		chunk := make([]byte, remaining)
		n, err := dev.Read(chunk)
		if err != nil {
			if errors.Is(err, ErrTimeout) {
				break
			}
			return buf, err
		}
		if n == 0 {
			break
		}
		buf = append(buf, chunk[:n]...)
		if n < remaining {
			// Short packet: the device has nothing more to give right now.
			break
		}
	}
	return buf, nil
}

func readStatus(dev BulkDevice) (byte, error) {
	buf, err := readExact(dev, 1)
	if err != nil {
		return 0, err
	}
	if len(buf) == 0 {
		return 0, &ScsiError{Message: "no status byte received"}
	}
	return buf[0], nil
}

func requestSense(dev BulkDevice) ([]byte, error) {
	if _, err := dev.Write(requestSenseCDB); err != nil {
		return nil, err
	}
	sense, err := readExact(dev, senseBufferLen)
	if err != nil {
		return nil, err
	}
	// Scanners commonly re-report a status byte here too (sometimes even
	// another check-condition); read and ignore it, matching the Python
	// implementation's "scanners commonly re-report INVAL here too" note.
	_, _ = readStatus(dev)
	return sense, nil
}

func writeAll(dev BulkDevice, p []byte) error {
	if len(p) == 0 {
		return nil
	}
	_, err := dev.Write(p)
	return err
}

// Command sends a CDB (zero-padded to MinCDBLen), optionally writes
// outData, optionally reads inLen bytes, then always reads the one-byte
// status the scanner sends after every command. It retries the whole
// exchange up to retries times (with a short pause) on transport-level
// errors, mirroring the reference Python implementation's avision_cmd().
//
// A CHECK CONDITION status (0x02) is not retried here and not treated as
// success or failure by this package — Command fetches the sense buffer
// and returns it as a *CheckConditionError alongside whatever data was
// already read. It is the caller's (device-specific package's)
// responsibility to classify that sense buffer: a "sense_key 0" condition
// generally means the command actually succeeded and the returned data is
// valid, while other codes may mean a graceful end-of-document or a real
// error. scsiusb intentionally has no opinion on that meaning.
func Command(dev BulkDevice, cdb, outData []byte, inLen int, retries int, retryDelay time.Duration) ([]byte, error) {
	cdb = PadCDB(cdb)
	var lastErr error

	for attempt := 0; attempt < retries; attempt++ {
		if err := writeAll(dev, cdb); err != nil {
			lastErr = err
			time.Sleep(retryDelay)
			continue
		}
		if err := writeAll(dev, outData); err != nil {
			lastErr = err
			time.Sleep(retryDelay)
			continue
		}

		var data []byte
		if inLen > 0 {
			d, err := readExact(dev, inLen)
			if err != nil {
				lastErr = err
				time.Sleep(retryDelay)
				continue
			}
			data = d
		}

		status, err := readStatus(dev)
		if err != nil {
			lastErr = err
			time.Sleep(retryDelay)
			continue
		}

		switch status {
		case StatusGood:
			return data, nil
		case StatusCheckCondition:
			sense, err := requestSense(dev)
			if err != nil {
				lastErr = err
				time.Sleep(retryDelay)
				continue
			}
			return data, &CheckConditionError{Sense: sense}
		case StatusBusy:
			lastErr = &ScsiError{Opcode: cdb[0], Message: "device busy"}
			time.Sleep(retryDelay)
			continue
		default:
			lastErr = &ScsiError{Opcode: cdb[0], Message: "unexpected status byte"}
			time.Sleep(retryDelay)
			continue
		}
	}

	return nil, &ScsiError{Opcode: cdb[0], Message: "command failed after retries: " + errString(lastErr)}
}

func errString(err error) string {
	if err == nil {
		return "no error recorded"
	}
	return err.Error()
}
