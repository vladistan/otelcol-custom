// Package fakeport is an in-memory, scriptable port.Port for tests and the
// swap suite. Unscripted device calls return NotSupported, like a consumer
// card without the feature.
package fakeport

import (
	"errors"
	"fmt"
	"sync"

	"github.com/vladistan/otelcol-custom/receiver/gpureceiver/internal/port"
)

// Call names a scriptable device call. PCIe, ECC and clock calls are split
// per direction, kind and domain.
type Call string

const (
	CallUtilization        Call = "Utilization"
	CallEncoderUtil        Call = "EncoderUtil"
	CallDecoderUtil        Call = "DecoderUtil"
	CallMemoryInfo         Call = "MemoryInfo"
	CallPCIeReceive        Call = "PCIeReceive"
	CallPCIeTransmit       Call = "PCIeTransmit"
	CallECCCorrected       Call = "ECCCorrected"
	CallECCUncorrected     Call = "ECCUncorrected"
	CallTemperature        Call = "Temperature"
	CallPowerUsage         Call = "PowerUsage"
	CallEnforcedPowerLimit Call = "EnforcedPowerLimit"
	CallClockGraphics      Call = "ClockGraphics"
	CallClockSM            Call = "ClockSM"
	CallClockMemory        Call = "ClockMemory"
	CallClockVideo         Call = "ClockVideo"
)

// Step is one scripted result: a value, or an error when Err is set.
type Step struct {
	Value any
	Err   error
}

// Ok scripts a successful result.
func Ok(value any) Step { return Step{Value: value} }

// Fail scripts a failure of the given kind.
func Fail(kind port.Kind) Step {
	return Step{Err: port.NewError(kind, errors.New("fake: "+kind.String()))}
}

type script struct {
	queue  []Step
	steady *Step
}

// Device is a scriptable GPU handle.
type Device struct {
	uuid, name       string
	uuidErr, nameErr error

	mu      sync.Mutex
	scripts map[Call]*script
	counts  map[Call]int
}

var _ port.Device = (*Device)(nil)

func newDevice(uuid, name string) *Device {
	return &Device{
		uuid:    uuid,
		name:    name,
		scripts: map[Call]*script{},
		counts:  map[Call]int{},
	}
}

func (d *Device) script(call Call) *script {
	s, ok := d.scripts[call]
	if !ok {
		s = &script{}
		d.scripts[call] = s
	}
	return s
}

// Set makes call return value once any queued steps are used up.
func (d *Device) Set(call Call, value any) *Device {
	d.mu.Lock()
	defer d.mu.Unlock()
	step := Ok(value)
	d.script(call).steady = &step
	return d
}

// Queue schedules steps for the next calls, in order, ahead of the steady value.
func (d *Device) Queue(call Call, steps ...Step) *Device {
	d.mu.Lock()
	defer d.mu.Unlock()
	s := d.script(call)
	s.queue = append(s.queue, steps...)
	return d
}

// FailUUID fails every UUID call, so enumeration excludes this GPU.
func (d *Device) FailUUID(kind port.Kind) *Device {
	d.uuidErr = Fail(kind).Err
	return d
}

// FailName fails every Name call, so enumeration excludes this GPU.
func (d *Device) FailName(kind port.Kind) *Device {
	d.nameErr = Fail(kind).Err
	return d
}

// Count returns how many times call ran.
func (d *Device) Count(call Call) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.counts[call]
}

func (d *Device) next(call Call) (any, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.counts[call]++
	s, ok := d.scripts[call]
	if !ok {
		return nil, Fail(port.NotSupported).Err
	}
	if len(s.queue) > 0 {
		step := s.queue[0]
		s.queue = s.queue[1:]
		return step.Value, step.Err
	}
	if s.steady != nil {
		return s.steady.Value, s.steady.Err
	}
	return nil, Fail(port.NotSupported).Err
}

func read[T any](d *Device, call Call) (T, error) {
	var zero T
	value, err := d.next(call)
	if err != nil {
		return zero, err
	}
	typed, ok := value.(T)
	if !ok {
		return zero, port.NewError(port.Other,
			fmt.Errorf("fake: %s scripted as %T, want %T", call, value, zero))
	}
	return typed, nil
}

func (d *Device) UUID() (string, error) { return d.uuid, d.uuidErr }

func (d *Device) Name() (string, error) { return d.name, d.nameErr }

func (d *Device) Utilization() (port.Utilization, error) {
	return read[port.Utilization](d, CallUtilization)
}

func (d *Device) EncoderUtil() (uint32, error) { return read[uint32](d, CallEncoderUtil) }

func (d *Device) DecoderUtil() (uint32, error) { return read[uint32](d, CallDecoderUtil) }

func (d *Device) MemoryInfo() (port.MemoryInfo, error) {
	return read[port.MemoryInfo](d, CallMemoryInfo)
}

func (d *Device) PCIeThroughput(dir port.Direction) (uint32, error) {
	if dir == port.Transmit {
		return read[uint32](d, CallPCIeTransmit)
	}
	return read[uint32](d, CallPCIeReceive)
}

func (d *Device) ECCCount(kind port.ECCKind) (uint64, error) {
	if kind == port.Uncorrected {
		return read[uint64](d, CallECCUncorrected)
	}
	return read[uint64](d, CallECCCorrected)
}

func (d *Device) Temperature() (uint32, error) { return read[uint32](d, CallTemperature) }

func (d *Device) PowerUsage() (uint32, error) { return read[uint32](d, CallPowerUsage) }

func (d *Device) EnforcedPowerLimit() (uint32, error) {
	return read[uint32](d, CallEnforcedPowerLimit)
}

func (d *Device) Clock(domain port.ClockDomain) (uint32, error) {
	switch domain {
	case port.ClockSM:
		return read[uint32](d, CallClockSM)
	case port.ClockMemory:
		return read[uint32](d, CallClockMemory)
	case port.ClockVideo:
		return read[uint32](d, CallClockVideo)
	default:
		return read[uint32](d, CallClockGraphics)
	}
}

// Port is a scriptable NVML library with N devices.
type Port struct {
	mu        sync.Mutex
	devices   []*Device
	version   string
	initErr   error
	countErr  error
	deviceErr map[int]error
	counts    map[string]int
}

var _ port.Port = (*Port)(nil)

// New returns an empty fake port.
func New() *Port {
	return &Port{deviceErr: map[int]error{}, counts: map[string]int{}}
}

// AddDevice appends a GPU and returns it for scripting.
func (p *Port) AddDevice(uuid, name string) *Device {
	p.mu.Lock()
	defer p.mu.Unlock()
	d := newDevice(uuid, name)
	p.devices = append(p.devices, d)
	return d
}

// SetDriverVersion is the string DriverVersion reports; the default is empty.
func (p *Port) SetDriverVersion(version string) *Port {
	p.version = version
	return p
}

// FailInit fails every Init call, as on a host with no usable driver.
func (p *Port) FailInit(kind port.Kind) *Port {
	p.initErr = Fail(kind).Err
	return p
}

// FailDeviceCount fails every DeviceCount call after a successful Init.
func (p *Port) FailDeviceCount(kind port.Kind) *Port {
	p.countErr = Fail(kind).Err
	return p
}

// FailDevice fails the handle lookup for one index on every call.
func (p *Port) FailDevice(index int, kind port.Kind) *Port {
	p.deviceErr[index] = Fail(kind).Err
	return p
}

// Calls returns how many times the named port method ran.
func (p *Port) Calls(method string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.counts[method]
}

func (p *Port) record(method string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.counts[method]++
}

func (p *Port) Init() error {
	p.record("Init")
	return p.initErr
}

func (p *Port) Shutdown() { p.record("Shutdown") }

func (p *Port) DriverVersion() (string, error) {
	p.record("DriverVersion")
	return p.version, nil
}

func (p *Port) DeviceCount() (int, error) {
	p.record("DeviceCount")
	if p.countErr != nil {
		return 0, p.countErr
	}
	return len(p.devices), nil
}

func (p *Port) Device(index int) (port.Device, error) {
	p.record("Device")
	if err := p.deviceErr[index]; err != nil {
		return nil, err
	}
	if index < 0 || index >= len(p.devices) {
		return nil, port.NewError(port.Other, fmt.Errorf("fake: no device %d", index))
	}
	return p.devices[index], nil
}
