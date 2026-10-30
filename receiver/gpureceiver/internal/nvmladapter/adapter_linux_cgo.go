// Modified: reshaped from internal/nvml into a port.Port adapter; raw NVML units, error kinds.
// Original: https://github.com/pmady/otel-gpu-receiver@ac756935ca040923d98af5a0d0547dca02a3b23b/internal/nvml/real.go

//go:build linux && cgo

package nvmladapter

import (
	gonvml "github.com/NVIDIA/go-nvml/pkg/nvml"

	"github.com/vladistan/otelcol-custom/receiver/gpureceiver/internal/port"
)

var (
	_ port.Port   = (*adapter)(nil)
	_ port.Device = (*device)(nil)
)

type adapter struct{}

type device struct {
	handle gonvml.Device
}

// kindOf folds frequency and argument-version codes into NotSupported and
// VersionMismatch; every code the table omits is Other.
func kindOf(ret gonvml.Return) port.Kind {
	switch ret {
	case gonvml.ERROR_NOT_SUPPORTED, gonvml.ERROR_FREQ_NOT_SUPPORTED:
		return port.NotSupported
	case gonvml.ERROR_GPU_IS_LOST:
		return port.GPULost
	case gonvml.ERROR_LIBRARY_NOT_FOUND:
		return port.LibraryNotFound
	case gonvml.ERROR_DRIVER_NOT_LOADED:
		return port.DriverNotLoaded
	case gonvml.ERROR_NO_PERMISSION:
		return port.NoPermission
	case gonvml.ERROR_LIB_RM_VERSION_MISMATCH, gonvml.ERROR_ARGUMENT_VERSION_MISMATCH:
		return port.VersionMismatch
	default:
		return port.Other
	}
}

func check(ret gonvml.Return) error {
	if ret == gonvml.SUCCESS {
		return nil
	}
	return port.NewError(kindOf(ret), ret)
}

func (a *adapter) Init() error { return check(gonvml.Init()) }

// Shutdown drops the NVML return code: the C-6 contract has Shutdown return
// nothing, and the process is exiting.
func (a *adapter) Shutdown() { _ = gonvml.Shutdown() }

func (a *adapter) DriverVersion() (string, error) {
	version, ret := gonvml.SystemGetDriverVersion()
	return version, check(ret)
}

func (a *adapter) DeviceCount() (int, error) {
	count, ret := gonvml.DeviceGetCount()
	return count, check(ret)
}

func (a *adapter) Device(index int) (port.Device, error) {
	handle, ret := gonvml.DeviceGetHandleByIndex(index)
	if err := check(ret); err != nil {
		return nil, err
	}
	return &device{handle: handle}, nil
}

func (d *device) UUID() (string, error) {
	uuid, ret := d.handle.GetUUID()
	return uuid, check(ret)
}

func (d *device) Name() (string, error) {
	name, ret := d.handle.GetName()
	return name, check(ret)
}

func (d *device) Utilization() (port.Utilization, error) {
	rates, ret := d.handle.GetUtilizationRates()
	return port.Utilization{GPU: rates.Gpu, Memory: rates.Memory}, check(ret)
}

func (d *device) EncoderUtil() (uint32, error) {
	util, _, ret := d.handle.GetEncoderUtilization()
	return util, check(ret)
}

func (d *device) DecoderUtil() (uint32, error) {
	util, _, ret := d.handle.GetDecoderUtilization()
	return util, check(ret)
}

func (d *device) MemoryInfo() (port.MemoryInfo, error) {
	info, ret := d.handle.GetMemoryInfo()
	return port.MemoryInfo{Used: info.Used, Total: info.Total}, check(ret)
}

func (d *device) PCIeThroughput(dir port.Direction) (uint32, error) {
	var counter gonvml.PcieUtilCounter
	switch dir {
	case port.Receive:
		counter = gonvml.PCIE_UTIL_RX_BYTES
	case port.Transmit:
		counter = gonvml.PCIE_UTIL_TX_BYTES
	default:
		return 0, port.NewError(port.Other, nil)
	}
	kbps, ret := d.handle.GetPcieThroughput(counter)
	return kbps, check(ret)
}

func (d *device) ECCCount(kind port.ECCKind) (uint64, error) {
	var errorType gonvml.MemoryErrorType
	switch kind {
	case port.Corrected:
		errorType = gonvml.MEMORY_ERROR_TYPE_CORRECTED
	case port.Uncorrected:
		errorType = gonvml.MEMORY_ERROR_TYPE_UNCORRECTED
	default:
		return 0, port.NewError(port.Other, nil)
	}
	count, ret := d.handle.GetTotalEccErrors(errorType, gonvml.VOLATILE_ECC)
	return count, check(ret)
}

func (d *device) Temperature() (uint32, error) {
	celsius, ret := d.handle.GetTemperature(gonvml.TEMPERATURE_GPU)
	return celsius, check(ret)
}

func (d *device) PowerUsage() (uint32, error) {
	milliwatts, ret := d.handle.GetPowerUsage()
	return milliwatts, check(ret)
}

func (d *device) EnforcedPowerLimit() (uint32, error) {
	milliwatts, ret := d.handle.GetEnforcedPowerLimit()
	return milliwatts, check(ret)
}

func (d *device) Clock(domain port.ClockDomain) (uint32, error) {
	var clock gonvml.ClockType
	switch domain {
	case port.ClockGraphics:
		clock = gonvml.CLOCK_GRAPHICS
	case port.ClockSM:
		clock = gonvml.CLOCK_SM
	case port.ClockMemory:
		clock = gonvml.CLOCK_MEM
	case port.ClockVideo:
		clock = gonvml.CLOCK_VIDEO
	default:
		return 0, port.NewError(port.Other, nil)
	}
	mhz, ret := d.handle.GetClockInfo(clock)
	return mhz, check(ret)
}
