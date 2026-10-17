// Package semconv holds the emitted metric names, attribute keys and values,
// units and toggle keys. Names follow OpenTelemetry semantic conventions
// v1.44.0 except the three extensions marked below.
package semconv

// SchemaVersion is the pinned semantic-conventions tag.
const SchemaVersion = "v1.44.0"

// Metric names. The first nine are semconv; the last three are extensions.
const (
	MetricUtilization       = "hw.gpu.utilization"
	MetricMemoryUsage       = "hw.gpu.memory.usage"
	MetricMemoryLimit       = "hw.gpu.memory.limit"
	MetricMemoryUtilization = "hw.gpu.memory.utilization"
	MetricIO                = "hw.gpu.io"
	MetricErrors            = "hw.errors"
	MetricStatus            = "hw.status"
	MetricTemperature       = "hw.temperature"
	MetricPower             = "hw.power"

	MetricClock          = "hw.gpu.clock"
	MetricPowerLimit     = "hw.gpu.power.limit"
	MetricPCIeThroughput = "hw.gpu.pcie.throughput"
)

const (
	UnitRatio   = "1"
	UnitBytes   = "By"
	UnitError   = "{error}"
	UnitCelsius = "Cel"
	UnitWatt    = "W"
	UnitHertz   = "Hz"
	UnitBytesPS = "By/s"
)

const (
	AttrID             = "hw.id"
	AttrName           = "hw.name"
	AttrModel          = "hw.model"
	AttrType           = "hw.type"
	AttrVendor         = "hw.vendor"
	AttrDriverVersion  = "hw.driver_version"
	AttrParent         = "hw.parent"
	AttrSensorLocation = "hw.sensor_location"
	AttrState          = "hw.state"
	AttrGPUTask        = "hw.gpu.task"
	AttrClockDomain    = "hw.gpu.clock.domain"
	AttrNetworkDir     = "network.io.direction"
	AttrErrorType      = "error.type"
)

// Attribute values.
const (
	TypeGPU      = "gpu"
	VendorNvidia = "nvidia"

	TaskGeneral = "general"
	TaskEncoder = "encoder"
	TaskDecoder = "decoder"

	StateOK       = "ok"
	StateDegraded = "degraded"
	StateFailed   = "failed"

	DirReceive  = "receive"
	DirTransmit = "transmit"

	ErrorCorrected   = "corrected"
	ErrorUncorrected = "uncorrected"

	ClockGraphics = "graphics"
	ClockSM       = "sm"
	ClockMemory   = "memory"
	ClockVideo    = "video"

	SensorLocationGPU = "gpu"
)

// ToggleKeys returns every metric name that has an enabled toggle, in schema
// order. Toggle keys equal metric names (C-2).
func ToggleKeys() []string {
	return []string{
		MetricUtilization,
		MetricMemoryUsage,
		MetricMemoryLimit,
		MetricMemoryUtilization,
		MetricIO,
		MetricErrors,
		MetricStatus,
		MetricTemperature,
		MetricPower,
		MetricClock,
		MetricPowerLimit,
		MetricPCIeThroughput,
	}
}

// Extensions returns the metric names that are not in semconv v1.44.0.
func Extensions() []string {
	return []string{MetricClock, MetricPowerLimit, MetricPCIeThroughput}
}
