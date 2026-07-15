package doxiedx400

import (
	"errors"

	"github.com/andiapps-dev/doxie-scanner/internal/scsiusb"
)

// scanOnePage scans a single sheet already confirmed present in the ADF
// (callers must check hasPaper first). It resends the full per-page
// setup (window, calibration, color matrix, gamma, attach-truncate)
// every time, since it isn't known whether this device supports keeping
// settings across pages — resending is cheap and matches the one
// proven-working sequence exactly.
//
// If the image-data read loop fails with a genuine error (not a graceful
// ScanComplete end-of-document), this returns immediately without
// running the REQUEST SENSE/RELEASE UNIT cleanup that follows a normal
// finish — matching the reference implementation's behavior exactly,
// including that gap.
func scanOnePage(dev scsiusb.BulkDevice, duplex bool) ([][]byte, error) {
	if err := setWindow(dev, duplex); err != nil {
		return nil, err
	}
	if _, err := readCalibrationFormat(dev); err != nil {
		return nil, err
	}
	if err := sendColorMatrix(dev); err != nil {
		return nil, err
	}
	for channel := byte(0); channel < 3; channel++ {
		if err := sendGammaChannel(dev, channel); err != nil {
			return nil, err
		}
	}
	if err := sendAttachTruncate(dev, datatypeAttachTruncTail); err != nil {
		return nil, err
	}
	if err := sendAttachTruncate(dev, datatypeAttachTruncHead); err != nil {
		return nil, err
	}
	if err := reserveUnit(dev); err != nil {
		return nil, err
	}
	if err := startScan(dev); err != nil {
		return nil, err
	}

	var chunks [][]byte
	for {
		data, err := readImageChunk(dev)
		if err != nil {
			var sc *scsiusb.ScanComplete
			if errors.As(err, &sc) {
				break
			}
			return nil, err
		}
		if len(data) == 0 {
			break
		}
		chunks = append(chunks, data)
	}

	if _, err := requestSenseCmd(dev); err != nil {
		return nil, err
	}
	if err := releaseUnit(dev, 0x00); err != nil {
		return nil, err
	}
	if err := releaseUnit(dev, 0x01); err != nil {
		return nil, err
	}

	return chunks, nil
}
