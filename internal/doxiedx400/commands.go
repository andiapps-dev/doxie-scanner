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

// command sends a CDB (with optional outData/inLen) via scsiusb.Command
// and classifies any resulting check-condition sense data using this
// device's own sense meanings. A sense_key of 0 is treated as an ordinary
// success (the data scsiusb already read back is returned as-is); any
// other classification (ScanComplete or a real ScsiError) is returned as
// this call's error.
func command(dev scsiusb.BulkDevice, cdb, outData []byte, inLen int) ([]byte, error) {
	data, err := scsiusb.Command(dev, cdb, outData, inLen, defaultRetries, defaultRetryDelay)
	if err == nil {
		return data, nil
	}

	var cc *scsiusb.CheckConditionError
	if errors.As(err, &cc) {
		if serr := classifySense(cc.Sense); serr != nil {
			return nil, serr
		}
		return data, nil
	}
	return nil, err
}

func inquiry(dev scsiusb.BulkDevice) ([]byte, error) {
	cdb := []byte{opInquiry, 0x00, 0x00, 0x00, 0x60, 0x00}
	return command(dev, cdb, nil, 0x60)
}

func mediaCheck(dev scsiusb.BulkDevice) ([]byte, error) {
	cdb := []byte{opMediaCheck, 0x00, 0x00, 0x00, 0x01, 0x00}
	return command(dev, cdb, nil, 1)
}

// hasPaper reports whether the ADF currently has a sheet ready to scan:
// bit 0 of the MEDIA_CHECK response byte. This is exactly what
// avision.c's sane_start() checks before every page, and how
// `scanimage --batch` knows when a multi-page batch is done.
func hasPaper(dev scsiusb.BulkDevice) (bool, error) {
	data, err := mediaCheck(dev)
	if err != nil {
		return false, err
	}
	return len(data) > 0 && data[0]&0x01 != 0, nil
}

func setWindow(dev scsiusb.BulkDevice, duplex bool) error {
	wd := windowData
	if duplex {
		wd = windowDataDuplex
	}
	cdb := []byte{opSetWindow, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, byte(len(wd)), 0x00}
	_, err := command(dev, cdb, wd, 0)
	return err
}

func readCalibrationFormat(dev scsiusb.BulkDevice) ([]byte, error) {
	cdb := []byte{
		opRead, 0x00, datatypeGetCalibrationFmt, 0x00,
		byte(dataDQ >> 8), byte(dataDQ & 0xff),
		0x00, 0x00, 0x20,
	}
	return command(dev, cdb, nil, 0x20)
}

func sendColorMatrix(dev scsiusb.BulkDevice) error {
	cdb := []byte{opSend, 0x00, datatype3x3ColorMatrix, 0x00, 0x00, 0x00, 0x00, 0x00, byte(len(colorMatrix))}
	_, err := command(dev, cdb, colorMatrix, 0)
	return err
}

func sendGammaChannel(dev scsiusb.BulkDevice, channel byte) error {
	cdb := []byte{opSend, 0x00, datatypeDownloadGamma, 0x00, 0x00, channel, 0x00, 0x02, 0x00}
	_, err := command(dev, cdb, gammaTable, 0)
	return err
}

func sendAttachTruncate(dev scsiusb.BulkDevice, datatypeCode byte) error {
	cdb := []byte{opSend, 0x00, datatypeCode, 0x00, 0x00, 0x01, 0x00, 0x00, 0x02}
	_, err := command(dev, cdb, []byte{0x00, 0x00}, 0)
	return err
}

func reserveUnit(dev scsiusb.BulkDevice) error {
	_, err := command(dev, []byte{opReserveUnit}, nil, 0)
	return err
}

func releaseUnit(dev scsiusb.BulkDevice, which byte) error {
	cdb := []byte{opReleaseUnit, 0x00, 0x00, 0x00, 0x00, which}
	_, err := command(dev, cdb, nil, 0)
	return err
}

func startScan(dev scsiusb.BulkDevice) error {
	cdb := []byte{opScan, 0x00, 0x00, 0x00, 0x01, 0x80}
	_, err := command(dev, cdb, nil, 0)
	return err
}

func requestSenseCmd(dev scsiusb.BulkDevice) ([]byte, error) {
	cdb := []byte{opRequestSense, 0x00, 0x00, 0x00, senseBufferLen}
	return command(dev, cdb, nil, senseBufferLen)
}

func readImageChunk(dev scsiusb.BulkDevice) ([]byte, error) {
	cdb := []byte{
		opRead, 0x00, datatypeReadImageData, 0x00,
		byte(dataDQ >> 8), byte(dataDQ & 0xff),
		byte(readChunkBytes >> 16), byte((readChunkBytes >> 8) & 0xff), byte(readChunkBytes & 0xff),
	}
	return command(dev, cdb, nil, readChunkBytes)
}
