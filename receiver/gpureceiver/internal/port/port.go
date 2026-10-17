package port

import (
	"time"

	"go.opentelemetry.io/collector/pdata/pmetric"
)

// Direction selects the PCIe or network traffic direction.
type Direction int

const (
	Receive Direction = iota + 1
	Transmit
)

// ECCKind selects the volatile ECC counter: single-bit (Corrected) or
// double-bit (Uncorrected).
type ECCKind int

const (
	Corrected ECCKind = iota + 1
	Uncorrected
)

// ClockDomain selects the clock reported by Device.Clock.
type ClockDomain int

const (
	ClockGraphics ClockDomain = iota + 1
	ClockSM
	ClockMemory
	ClockVideo
)

// Utilization holds raw percentages, 0-100.
type Utilization struct {
	GPU    uint32
	Memory uint32
}

// MemoryInfo holds bytes.
type MemoryInfo struct {
	Used  uint64
	Total uint64
}

// Port is the NVML library. Every method returns raw NVML units and, on
// failure, an error whose Kind is read with KindOf.
type Port interface {
	Init() error
	Shutdown()
	DriverVersion() (string, error)
	DeviceCount() (int, error)
	Device(index int) (Device, error)
}

// Device is one GPU handle. Units are the NVML ones: percent, bytes, KB/s
// (PCIeThroughput), Celsius, milliwatts, MHz.
type Device interface {
	UUID() (string, error)
	Name() (string, error)
	Utilization() (Utilization, error)
	EncoderUtil() (uint32, error)
	DecoderUtil() (uint32, error)
	MemoryInfo() (MemoryInfo, error)
	PCIeThroughput(dir Direction) (uint32, error)
	ECCCount(kind ECCKind) (uint64, error)
	Temperature() (uint32, error)
	PowerUsage() (uint32, error)
	EnforcedPowerLimit() (uint32, error)
	Clock(domain ClockDomain) (uint32, error)
}

// DeviceIdentity is the static identity read once at enumeration.
type DeviceIdentity struct {
	Index         int
	UUID          string
	Model         string
	Name          string
	DriverVersion string
}

// Toggles maps an emitted metric name to its enabled flag.
type Toggles map[string]bool

// Collector builds one metrics batch per scrape. It never fails: device
// errors are logged and the affected series omitted.
type Collector interface {
	Collect(now time.Time) pmetric.Metrics
}
