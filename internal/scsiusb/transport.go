package scsiusb

import "errors"

// Status codes carried in the last byte of every command's status reply.
const (
	StatusGood           byte = 0x00
	StatusCheckCondition byte = 0x02
	StatusBusy           byte = 0x08
)

// ErrTimeout is returned by a BulkDevice.Read implementation when a read
// times out with no data; readExact treats it as "nothing more pending".
var ErrTimeout = errors.New("scsiusb: read timed out")

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
			// A short packet: the device has nothing more for this phase.
			break
		}
	}
	return buf, nil
}

func writeAll(dev BulkDevice, p []byte) error {
	if len(p) == 0 {
		return nil
	}
	_, err := dev.Write(p)
	return err
}

func errString(err error) string {
	if err == nil {
		return "no error recorded"
	}
	return err.Error()
}
