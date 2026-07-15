package doxiedx400

import (
	"fmt"

	"github.com/andiapps-dev/doxie-scanner/internal/scsiusb"
)

// classifySense mirrors avision.c's sense_handler() for the specific
// codes this device is known to use: sense_key 0 means the command that
// triggered the check condition actually succeeded and its data is
// valid; ASC/ASCQ (0x80,0x03) "ADF chute empty" or (0x80,0x04) "ADF paper
// end" are a normal end-of-document signal, not an error; anything else
// is a real scanner-reported failure.
func classifySense(sense []byte) error {
	if len(sense) < 14 {
		return &scsiusb.ScsiError{Message: fmt.Sprintf("short sense buffer: %d bytes", len(sense))}
	}

	senseKey := sense[2] & 0x0f
	asc, ascq := sense[12], sense[13]

	if senseKey == 0x00 {
		return nil
	}
	if asc == 0x80 && (ascq == 0x03 || ascq == 0x04) {
		return &scsiusb.ScanComplete{
			Reason: fmt.Sprintf("end of document (asc=%#02x ascq=%#02x)", asc, ascq),
		}
	}
	return &scsiusb.ScsiError{
		Message: fmt.Sprintf("scanner reported error: sense_key=%#02x asc=%#02x ascq=%#02x", senseKey, asc, ascq),
	}
}
