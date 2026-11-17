package scsiusb

import (
	"fmt"
	"time"
)

// Avision's newer USB framing, as used by the vendor DX400 driver: a
// 12-byte CDB, an optional data phase, then a 3-byte status whose last
// byte carries the classic status code. Check conditions are followed up
// with the vendor sense command (0xE3) instead of SCSI REQUEST SENSE.
const (
	CDB12Len     = 12
	Sense12Len   = 0x2C
	status12Len  = 3
	statusCodeAt = 2
	sense12Op    = 0xE3
	maxLength24  = 1<<24 - 1
)

// CDB12 builds a 12-byte command: data type at byte 4, a 24-bit
// big-endian transfer length at bytes 8–10, and a flags byte at 11.
func CDB12(opcode, dataType byte, length int, flags byte) []byte {
	if length < 0 || length > maxLength24 {
		panic("scsiusb: CDB12 length out of range")
	}
	cdb := make([]byte, CDB12Len)
	cdb[0] = opcode
	cdb[4] = dataType
	cdb[8] = byte(length >> 16)
	cdb[9] = byte(length >> 8)
	cdb[10] = byte(length)
	cdb[11] = flags
	return cdb
}

// Command12 runs one 12-byte exchange. Pass retries=1 for image reads:
// re-sending a READ would silently skip a chunk of image data.
func Command12(dev BulkDevice, cdb, outData []byte, inLen int, retries int, retryDelay time.Duration) ([]byte, error) {
	if len(cdb) != CDB12Len {
		return nil, &ScsiError{Message: fmt.Sprintf("CDB must be %d bytes, got %d", CDB12Len, len(cdb))}
	}
	var lastErr error

	for attempt := 0; attempt < retries; attempt++ {
		if attempt > 0 {
			time.Sleep(retryDelay)
		}
		if err := sendCDB(dev, cdb); err != nil {
			lastErr = err
			continue
		}
		if err := writeAll(dev, outData); err != nil {
			lastErr = err
			continue
		}

		var data []byte
		if inLen > 0 {
			d, err := readExact(dev, inLen)
			if err != nil {
				lastErr = err
				continue
			}
			data = d
		}

		status, err := readStatus12(dev)
		if err != nil {
			lastErr = err
			continue
		}

		switch status[statusCodeAt] {
		case StatusGood:
			return data, nil
		case StatusCheckCondition:
			sense, err := requestSense12(dev)
			if err != nil {
				lastErr = err
				continue
			}
			return data, &CheckConditionError{Sense: sense}
		case StatusBusy:
			lastErr = &ScsiError{Opcode: cdb[0], Message: "device busy"}
		default:
			lastErr = &ScsiError{Opcode: cdb[0], Message: fmt.Sprintf("unexpected status % x", status)}
		}
	}

	return nil, &ScsiError{Opcode: cdb[0], Message: "command failed after retries: " + errString(lastErr)}
}

func readStatus12(dev BulkDevice) ([]byte, error) {
	buf, err := readExact(dev, status12Len)
	if err != nil {
		return nil, err
	}
	if len(buf) != status12Len {
		return nil, &ScsiError{Message: fmt.Sprintf("short status: % x", buf)}
	}
	return buf, nil
}

func sendCDB(dev BulkDevice, cdb []byte) error {
	if s, ok := dev.(CommandSender); ok {
		return s.SendCommand(cdb)
	}
	return writeAll(dev, cdb)
}

func requestSense12(dev BulkDevice) ([]byte, error) {
	if err := sendCDB(dev, CDB12(sense12Op, 0, Sense12Len, 0)); err != nil {
		return nil, err
	}
	sense, err := readExact(dev, Sense12Len)
	if err != nil {
		return nil, err
	}
	_, _ = readStatus12(dev)
	return sense, nil
}
