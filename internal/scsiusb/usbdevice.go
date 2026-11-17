package scsiusb

import (
	"context"
	"fmt"
	"runtime"

	"github.com/google/gousb"
)

// USBDevice is the gousb-backed BulkDevice implementation that talks to
// real hardware. It is intentionally a thin adapter with no protocol
// logic of its own — open the device, claim an interface, hand back
// endpoint handles — so that essentially none of this codebase's actual
// behavior lives in code that can only be exercised against real USB
// hardware. See cdb.go/errors.go for the logic this wraps, which is fully
// unit-tested against a fake BulkDevice instead.
type USBDevice struct {
	ctx   *gousb.Context
	dev   *gousb.Device
	cfg   *gousb.Config
	intf  *gousb.Interface
	inEP  *gousb.InEndpoint
	outEP *gousb.OutEndpoint
	intEP *gousb.InEndpoint
}

// Open finds a USB device by VID/PID, claims its interface, and returns a
// BulkDevice ready to exchange data over the given bulk endpoint numbers
// (an EndpointAddress like 0x81 encodes both the endpoint number and
// direction; per gousb's convention, address 0x81 is IN endpoint number
// 1, and address 0x02 is OUT endpoint number 2 — pass the plain endpoint
// numbers here, not the raw address bytes).
//
// gousb.NewContext panics (rather than returning an error) if the
// underlying libusb_init call fails — which happens in practice whenever
// /dev/bus/usb isn't accessible, e.g. a container run without USB
// passthrough. That's a foreseeable, ordinary misconfiguration, not a
// programming error, so it's recovered here and reported the same way as
// every other "couldn't get at the device" failure instead of crashing
// the calling HTTP request.
func Open(vid, pid uint16, interfaceNum, inEPNum, outEPNum, intEPNum int) (result *USBDevice, err error) {
	defer func() {
		if r := recover(); r != nil {
			result = nil
			err = &ErrClaimInterface{Cause: fmt.Errorf("libusb initialization failed: %v", r)}
		}
	}()
	return openDevice(vid, pid, interfaceNum, inEPNum, outEPNum, intEPNum)
}

func openDevice(vid, pid uint16, interfaceNum, inEPNum, outEPNum, intEPNum int) (*USBDevice, error) {
	ctx := gousb.NewContext()

	dev, err := ctx.OpenDeviceWithVIDPID(gousb.ID(vid), gousb.ID(pid))
	if err != nil {
		ctx.Close()
		return nil, &ErrClaimInterface{Cause: err}
	}
	if dev == nil {
		ctx.Close()
		return nil, &ErrDeviceNotFound{VID: vid, PID: pid}
	}

	// Detaching a kernel driver needs root on macOS, where nothing is bound
	// to this interface anyway; on Linux it guards against usblp-style
	// drivers grabbing the scanner.
	if runtime.GOOS == "linux" {
		if err := dev.SetAutoDetach(true); err != nil {
			dev.Close()
			ctx.Close()
			return nil, &ErrClaimInterface{Cause: fmt.Errorf("set auto detach: %w", err)}
		}
	}

	cfgNum, err := firstConfigNumber(dev)
	if err != nil {
		dev.Close()
		ctx.Close()
		return nil, &ErrClaimInterface{Cause: err}
	}

	cfg, err := dev.Config(cfgNum)
	if err != nil {
		dev.Close()
		ctx.Close()
		return nil, &ErrClaimInterface{Cause: fmt.Errorf("select config %d: %w", cfgNum, err)}
	}

	intf, err := cfg.Interface(interfaceNum, 0)
	if err != nil {
		cfg.Close()
		dev.Close()
		ctx.Close()
		return nil, &ErrClaimInterface{Cause: fmt.Errorf("claim interface %d: %w", interfaceNum, err)}
	}

	inEP, err := intf.InEndpoint(inEPNum)
	if err != nil {
		intf.Close()
		cfg.Close()
		dev.Close()
		ctx.Close()
		return nil, &ErrClaimInterface{Cause: fmt.Errorf("open in-endpoint %d: %w", inEPNum, err)}
	}

	outEP, err := intf.OutEndpoint(outEPNum)
	if err != nil {
		intf.Close()
		cfg.Close()
		dev.Close()
		ctx.Close()
		return nil, &ErrClaimInterface{Cause: fmt.Errorf("open out-endpoint %d: %w", outEPNum, err)}
	}

	intEP, err := intf.InEndpoint(intEPNum)
	if err != nil {
		intf.Close()
		cfg.Close()
		dev.Close()
		ctx.Close()
		return nil, &ErrClaimInterface{Cause: fmt.Errorf("open interrupt endpoint %d: %w", intEPNum, err)}
	}

	return &USBDevice{ctx: ctx, dev: dev, cfg: cfg, intf: intf, inEP: inEP, outEP: outEP, intEP: intEP}, nil
}

func firstConfigNumber(dev *gousb.Device) (int, error) {
	for num := range dev.Desc.Configs {
		return num, nil
	}
	return 0, fmt.Errorf("device reports no USB configurations")
}

const (
	vendorOutToDevice = 0x40
	vendorCommandReq  = 0x04
)

// SendCommand delivers a CDB as the vendor control request Doxie's own
// driver uses (bmRequestType 0x40, bRequest 4).
func (u *USBDevice) SendCommand(cdb []byte) error {
	n, err := u.dev.Control(vendorOutToDevice, vendorCommandReq, 0, 0, cdb)
	if err != nil {
		return err
	}
	if n != len(cdb) {
		return &ScsiError{Opcode: cdb[0], Message: fmt.Sprintf("control transfer sent %d of %d bytes", n, len(cdb))}
	}
	return nil
}

func (u *USBDevice) Write(p []byte) (int, error) { return u.outEP.Write(p) }
func (u *USBDevice) Read(p []byte) (int, error)  { return u.inEP.Read(p) }

// ReadInterrupt waits on the interrupt IN pipe until a packet arrives or
// ctx ends.
func (u *USBDevice) ReadInterrupt(ctx context.Context, p []byte) (int, error) {
	return u.intEP.ReadContext(ctx, p)
}

func (u *USBDevice) Close() error {
	u.intf.Close()
	u.cfg.Close()
	err := u.dev.Close()
	u.ctx.Close()
	return err
}
