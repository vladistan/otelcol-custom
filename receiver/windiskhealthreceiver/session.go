// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package windiskhealthreceiver

import (
	"errors"
	"fmt"
	"io"
	"math"
	"time"

	"github.com/vladistan/otelcol-custom/receiver/smartdecode"
)

// session is one open disk. The ATA and NVMe methods are the transports smartdecode reads through.
type session interface {
	smartdecode.ATATransport
	smartdecode.NVMeTransport
	io.Closer
	descriptor() (descriptor, error)
	temperature() (int, error)
	predictFailure() (bool, error)
}

// bus lists the disks and opens one. The Windows build probes \\.\PhysicalDriveN; tests give fakes.
type bus interface {
	discover() ([]string, error)
	open(name string, timeout time.Duration) (session, error)
}

// controller makes one DeviceIoControl call. The Windows build holds the disk handle.
type controller interface {
	control(code uint32, in []byte, outLen int) ([]byte, error)
	io.Closer
}

// diskSession builds each request, checks it against the allow list, and decodes the answer.
type diskSession struct {
	c       controller
	timeout time.Duration
}

func (s *diskSession) Close() error { return s.c.Close() }

func (s *diskSession) send(r request) ([]byte, error) {
	if err := checkRequest(r); err != nil {
		return nil, err
	}
	return s.c.control(r.code, r.in, r.outLen)
}

func (s *diskSession) descriptor() (descriptor, error) {
	out, err := s.send(deviceRequest())
	if err != nil {
		return descriptor{}, fmt.Errorf("query the device descriptor: %w", err)
	}
	return decodeDescriptor(out)
}

func (s *diskSession) temperature() (int, error) {
	out, err := s.send(temperatureRequest())
	if err != nil {
		return 0, fmt.Errorf("query the temperature: %w", err)
	}
	return decodeTemperature(out)
}

func (s *diskSession) predictFailure() (bool, error) {
	out, err := s.send(predictFailureRequest())
	if err != nil {
		return false, fmt.Errorf("query the failure prediction: %w", err)
	}
	return decodePredictFailure(out)
}

// ATA sends one command that smartdecode allows, as ATA pass-through.
func (s *diskSession) ATA(cmd smartdecode.ATACommand) (smartdecode.ATAResult, error) {
	if err := smartdecode.CheckATA(cmd); err != nil {
		return smartdecode.ATAResult{}, err
	}
	secs := uint32(min(math.Ceil(s.timeout.Seconds()), math.MaxUint32))
	out, err := s.send(ataRequest(cmd, secs))
	if err != nil {
		return smartdecode.ATAResult{}, fmt.Errorf("send ATA pass-through: %w", err)
	}
	return decodeATA(out, cmd)
}

// Admin sends Identify Controller or the SMART/Health log page as a protocol-specific storage query.
func (s *diskSession) Admin(cmd smartdecode.NVMeCommand) ([]byte, error) {
	if err := smartdecode.CheckNVMe(cmd); err != nil {
		return nil, err
	}
	req := nvmeHealthRequest()
	if cmd == smartdecode.IdentifyControllerCommand() {
		req = nvmeIdentifyRequest()
	}
	out, err := s.send(req)
	if err != nil {
		return nil, fmt.Errorf("send protocol query: %w", err)
	}
	return decodeProtocolData(out, cmd.DataLen)
}

// Win32 error numbers that carry a meaning for the receiver.
const (
	win32AccessDenied       = 5
	win32PrivilegeNotHeld   = 1314
	win32InvalidFunction    = 1
	win32NotSupported       = 50
	win32InvalidParameter   = 87
	win32GeneralFailure     = 31
	win32IODevice           = 1117
	win32DeviceNotConnected = 1167
	win32SemTimeout         = 121
	win32OperationAborted   = 995
)

// mapWin32 turns a Win32 error number into the smartdecode error the scraper sorts by.
func mapWin32(code uint32, err error) error {
	switch code {
	case win32AccessDenied, win32PrivilegeNotHeld:
		return fmt.Errorf("%w: %w; the collector must run as LocalSystem or with the privileges to open disks", smartdecode.ErrPermission, err)
	case win32SemTimeout, win32OperationAborted:
		return fmt.Errorf("%w: %w", smartdecode.ErrTimeout, err)
	case win32InvalidFunction, win32NotSupported, win32InvalidParameter, win32GeneralFailure, win32IODevice, win32DeviceNotConnected:
		return fmt.Errorf("%w: %w", smartdecode.ErrUnsupported, err)
	}
	return err
}

// errSystemOnly is what a build for another system answers.
var errSystemOnly = errors.New("the windiskhealth receiver runs on Windows only")
