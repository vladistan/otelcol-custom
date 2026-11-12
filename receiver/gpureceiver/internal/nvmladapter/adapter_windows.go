//go:build windows

package nvmladapter

import (
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/vladistan/otelcol-custom/receiver/gpureceiver/internal/port"
)

var (
	_ port.Port   = (*adapter)(nil)
	_ port.Device = (*device)(nil)
)

// Fixed enum arguments: NVML_VOLATILE_ECC and NVML_TEMPERATURE_GPU.
const (
	volatileECC    = 0
	temperatureGPU = 0
)

// Buffer sizes from nvml.h (NVML_DEVICE_NAME_V2_BUFFER_SIZE,
// NVML_DEVICE_UUID_V2_BUFFER_SIZE, NVML_SYSTEM_DRIVER_VERSION_BUFFER_SIZE).
const (
	nameBufferSize          = 96
	uuidBufferSize          = 96
	driverVersionBufferSize = 80
)

// nvmlProcs holds the NVML entry points, bound on first use.
type nvmlProcs struct {
	initialise         *windows.LazyProc
	shutdown           *windows.LazyProc
	driverVersion      *windows.LazyProc
	deviceCount        *windows.LazyProc
	handleByIndex      *windows.LazyProc
	uuid               *windows.LazyProc
	name               *windows.LazyProc
	utilizationRates   *windows.LazyProc
	encoderUtilization *windows.LazyProc
	decoderUtilization *windows.LazyProc
	memoryInfo         *windows.LazyProc
	pcieThroughput     *windows.LazyProc
	totalECCErrors     *windows.LazyProc
	temperature        *windows.LazyProc
	powerUsage         *windows.LazyProc
	enforcedPowerLimit *windows.LazyProc
	clockInfo          *windows.LazyProc
}

func newProcs(dll *windows.LazyDLL) *nvmlProcs {
	return &nvmlProcs{
		initialise:         dll.NewProc("nvmlInit_v2"),
		shutdown:           dll.NewProc("nvmlShutdown"),
		driverVersion:      dll.NewProc("nvmlSystemGetDriverVersion"),
		deviceCount:        dll.NewProc("nvmlDeviceGetCount_v2"),
		handleByIndex:      dll.NewProc("nvmlDeviceGetHandleByIndex_v2"),
		uuid:               dll.NewProc("nvmlDeviceGetUUID"),
		name:               dll.NewProc("nvmlDeviceGetName"),
		utilizationRates:   dll.NewProc("nvmlDeviceGetUtilizationRates"),
		encoderUtilization: dll.NewProc("nvmlDeviceGetEncoderUtilization"),
		decoderUtilization: dll.NewProc("nvmlDeviceGetDecoderUtilization"),
		memoryInfo:         dll.NewProc("nvmlDeviceGetMemoryInfo"),
		pcieThroughput:     dll.NewProc("nvmlDeviceGetPcieThroughput"),
		totalECCErrors:     dll.NewProc("nvmlDeviceGetTotalEccErrors"),
		temperature:        dll.NewProc("nvmlDeviceGetTemperature"),
		powerUsage:         dll.NewProc("nvmlDeviceGetPowerUsage"),
		enforcedPowerLimit: dll.NewProc("nvmlDeviceGetEnforcedPowerLimit"),
		clockInfo:          dll.NewProc("nvmlDeviceGetClockInfo"),
	}
}

// callProc calls an NVML function and maps a non-zero return code to a
// port.Error. A function the library lacks is NotSupported, so one missing
// entry point drops one metric instead of the receiver.
//
// The uintptr arguments may be unsafe.Pointer conversions of Go variables;
// uintptrescapes keeps those variables alive until the call returns.
//
//go:uintptrescapes
func callProc(proc *windows.LazyProc, args ...uintptr) error {
	if err := proc.Find(); err != nil {
		return port.NewError(port.NotSupported, err)
	}
	ret, _, _ := proc.Call(args...)
	return checkCode(uint32(ret))
}

type adapter struct {
	dll    *windows.LazyDLL
	procs  *nvmlProcs
	loaded bool
}

type device struct {
	procs  *nvmlProcs
	handle uintptr
}

// Init loads nvml.dll, then calls nvmlInit_v2. A load failure is LibraryNotFound.
func (a *adapter) Init() error {
	if err := a.dll.Load(); err != nil {
		return port.NewError(port.LibraryNotFound, err)
	}
	if err := callProc(a.procs.initialise); err != nil {
		return err
	}
	a.loaded = true
	return nil
}

// Shutdown drops the NVML return code: the C-6 contract has Shutdown return
// nothing, and the process is exiting.
func (a *adapter) Shutdown() {
	if !a.loaded {
		return
	}
	_ = callProc(a.procs.shutdown)
	a.loaded = false
}

func (a *adapter) DriverVersion() (string, error) {
	var buf [driverVersionBufferSize]byte
	err := callProc(a.procs.driverVersion, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return cString(buf[:]), err
}

func (a *adapter) DeviceCount() (int, error) {
	var count uint32
	err := callProc(a.procs.deviceCount, uintptr(unsafe.Pointer(&count)))
	return int(count), err
}

func (a *adapter) Device(index int) (port.Device, error) {
	var handle uintptr
	if err := callProc(a.procs.handleByIndex, uintptr(index), uintptr(unsafe.Pointer(&handle))); err != nil {
		return nil, err
	}
	return &device{procs: a.procs, handle: handle}, nil
}

func (d *device) UUID() (string, error) {
	var buf [uuidBufferSize]byte
	err := callProc(d.procs.uuid, d.handle, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return cString(buf[:]), err
}

func (d *device) Name() (string, error) {
	var buf [nameBufferSize]byte
	err := callProc(d.procs.name, d.handle, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return cString(buf[:]), err
}

func (d *device) Utilization() (port.Utilization, error) {
	var rates utilization
	err := callProc(d.procs.utilizationRates, d.handle, uintptr(unsafe.Pointer(&rates)))
	return port.Utilization{GPU: rates.gpu, Memory: rates.memory}, err
}

func (d *device) EncoderUtil() (uint32, error) {
	var util, periodUs uint32
	err := callProc(d.procs.encoderUtilization, d.handle,
		uintptr(unsafe.Pointer(&util)), uintptr(unsafe.Pointer(&periodUs)))
	return util, err
}

func (d *device) DecoderUtil() (uint32, error) {
	var util, periodUs uint32
	err := callProc(d.procs.decoderUtilization, d.handle,
		uintptr(unsafe.Pointer(&util)), uintptr(unsafe.Pointer(&periodUs)))
	return util, err
}

func (d *device) MemoryInfo() (port.MemoryInfo, error) {
	var info memoryInfo
	err := callProc(d.procs.memoryInfo, d.handle, uintptr(unsafe.Pointer(&info)))
	return port.MemoryInfo{Used: info.used, Total: info.total}, err
}

func (d *device) PCIeThroughput(dir port.Direction) (uint32, error) {
	counter, ok := pcieCounterOf(dir)
	if !ok {
		return 0, port.NewError(port.Other, nil)
	}
	var kbps uint32
	err := callProc(d.procs.pcieThroughput, d.handle, uintptr(counter), uintptr(unsafe.Pointer(&kbps)))
	return kbps, err
}

func (d *device) ECCCount(kind port.ECCKind) (uint64, error) {
	errorType, ok := eccErrorTypeOf(kind)
	if !ok {
		return 0, port.NewError(port.Other, nil)
	}
	var count uint64
	err := callProc(d.procs.totalECCErrors, d.handle,
		uintptr(errorType), uintptr(volatileECC), uintptr(unsafe.Pointer(&count)))
	return count, err
}

func (d *device) Temperature() (uint32, error) {
	var celsius uint32
	err := callProc(d.procs.temperature, d.handle, uintptr(temperatureGPU), uintptr(unsafe.Pointer(&celsius)))
	return celsius, err
}

func (d *device) PowerUsage() (uint32, error) {
	var milliwatts uint32
	err := callProc(d.procs.powerUsage, d.handle, uintptr(unsafe.Pointer(&milliwatts)))
	return milliwatts, err
}

func (d *device) EnforcedPowerLimit() (uint32, error) {
	var milliwatts uint32
	err := callProc(d.procs.enforcedPowerLimit, d.handle, uintptr(unsafe.Pointer(&milliwatts)))
	return milliwatts, err
}

func (d *device) Clock(domain port.ClockDomain) (uint32, error) {
	clock, ok := clockTypeOf(domain)
	if !ok {
		return 0, port.NewError(port.Other, nil)
	}
	var mhz uint32
	err := callProc(d.procs.clockInfo, d.handle, uintptr(clock), uintptr(unsafe.Pointer(&mhz)))
	return mhz, err
}
