package scsiusb

import (
	"fmt"

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
func Open(vid, pid uint16, interfaceNum, inEPNum, outEPNum int) (result *USBDevice, err error) {
	defer func() {
		if r := recover(); r != nil {
			result = nil
			err = &ErrClaimInterface{Cause: fmt.Errorf("libusb initialization failed: %v", r)}
		}
	}()
	return openDevice(vid, pid, interfaceNum, inEPNum, outEPNum)
}

func openDevice(vid, pid uint16, interfaceNum, inEPNum, outEPNum int) (*USBDevice, error) {
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

	if err := dev.SetAutoDetach(true); err != nil {
		dev.Close()
		ctx.Close()
		return nil, &ErrClaimInterface{Cause: fmt.Errorf("set auto detach: %w", err)}
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

	return &USBDevice{ctx: ctx, dev: dev, cfg: cfg, intf: intf, inEP: inEP, outEP: outEP}, nil
}

func firstConfigNumber(dev *gousb.Device) (int, error) {
	for num := range dev.Desc.Configs {
		return num, nil
	}
	return 0, fmt.Errorf("device reports no USB configurations")
}

func (u *USBDevice) Write(p []byte) (int, error) { return u.outEP.Write(p) }
func (u *USBDevice) Read(p []byte) (int, error)  { return u.inEP.Read(p) }

func (u *USBDevice) Close() error {
	u.intf.Close()
	u.cfg.Close()
	err := u.dev.Close()
	u.ctx.Close()
	return err
}
