// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package hwschema

// Metric names. The semantic conventions define the first six; the last three are our own names under
// hw.physical_disk and move to the registry names if it adds them.
const (
	MetricStatus                  = "hw.status"
	MetricErrors                  = "hw.errors"
	MetricTemperature             = "hw.temperature"
	MetricPhysicalDiskSmart       = "hw.physical_disk.smart"
	MetricPhysicalDiskEndurance   = "hw.physical_disk.endurance_utilization"
	MetricPhysicalDiskSize        = "hw.physical_disk.size"
	MetricPhysicalDiskPowerOnTime = "hw.physical_disk.power_on_time"
	MetricPhysicalDiskPowerCycles = "hw.physical_disk.power_cycles"
	MetricPhysicalDiskIO          = "hw.physical_disk.io"
)

// Units.
const (
	UnitRatio   = "1"
	UnitBytes   = "By"
	UnitCelsius = "Cel"
	UnitSeconds = "s"
	UnitErrors  = "{error}"
	UnitCycles  = "{cycle}"
)

// Attribute keys.
const (
	AttrID               = "hw.id"
	AttrName             = "hw.name"
	AttrParent           = "hw.parent"
	AttrModel            = "hw.model"
	AttrSerialNumber     = "hw.serial_number"
	AttrFirmwareVersion  = "hw.firmware_version"
	AttrVendor           = "hw.vendor"
	AttrType             = "hw.type"
	AttrState            = "hw.state"
	AttrSensorLocation   = "hw.sensor_location"
	AttrPhysicalDiskType = "hw.physical_disk.type"
	// AttrPhysicalDiskState is "remaining" on the endurance metric.
	AttrPhysicalDiskState = "hw.physical_disk.state"
	AttrSmartAttribute    = "hw.physical_disk.smart_attribute"
	// AttrSmartValueType is our own attribute: it keeps the normalized, worst, threshold and raw
	// values of one SMART attribute in the one semantic-convention gauge.
	AttrSmartValueType = "hw.physical_disk.smart_value_type"
	AttrErrorType      = "error.type"
	AttrDiskIODir      = "disk.io.direction"
)

// Values of hw.type.
const TypePhysicalDisk = "physical_disk"

// Values of hw.state, from the semantic conventions.
const (
	StateOK               = "ok"
	StateNeedsCleaning    = "needs_cleaning"
	StateDegraded         = "degraded"
	StatePredictedFailure = "predicted_failure"
	StateFailed           = "failed"
)

// States lists the hw.state values from least to most severe; a status metric emits one point for each.
var States = []string{StateOK, StateNeedsCleaning, StateDegraded, StatePredictedFailure, StateFailed}

// Values of hw.physical_disk.type, hw.physical_disk.state, disk.io.direction, error.type and
// hw.physical_disk.smart_value_type.
const (
	DiskSSD  = "SSD"
	DiskHDD  = "HDD"
	DiskNVMe = "NVMe"

	DiskStateRemaining = "remaining"

	DirectionRead  = "read"
	DirectionWrite = "write"

	ErrorMedia     = "media"
	ErrorBadSector = "bad_sector"
	ErrorCRC       = "crc"

	ValueNormalized = "normalized"
	ValueWorst      = "worst"
	ValueThreshold  = "threshold"
	ValueRaw        = "raw"
)

// Kind is the OpenTelemetry data type of a metric.
type Kind int

// The kinds the physical disk metrics use. A Sum is monotonic and cumulative.
const (
	Gauge Kind = iota
	Sum
)

// MetricSpec describes one metric.
type MetricSpec struct {
	Name        string
	Unit        string
	Kind        Kind
	Description string
}

// PhysicalDiskMetrics lists the metrics of the disk health receivers.
var PhysicalDiskMetrics = []MetricSpec{
	{MetricStatus, UnitRatio, Gauge, "Health state of the device; one point for each state, 1 for the current one."},
	{MetricErrors, UnitErrors, Sum, "Errors the device reports, by error.type."},
	{MetricTemperature, UnitCelsius, Gauge, "Temperature of a sensor, by hw.sensor_location."},
	{MetricPhysicalDiskSmart, UnitRatio, Gauge, "One value of one SMART attribute, by attribute and value type."},
	{MetricPhysicalDiskEndurance, UnitRatio, Gauge, "Fraction of the endurance of a solid state disk that is left."},
	{MetricPhysicalDiskSize, UnitBytes, Gauge, "Capacity of the disk."},
	{MetricPhysicalDiskPowerOnTime, UnitSeconds, Gauge, "Time the disk has been powered on."},
	{MetricPhysicalDiskPowerCycles, UnitCycles, Sum, "Number of power cycles of the disk."},
	{MetricPhysicalDiskIO, UnitBytes, Sum, "Data the host read from or wrote to the disk, by disk.io.direction."},
}

// LookupMetric returns the spec of a metric name.
func LookupMetric(name string) (MetricSpec, bool) {
	for _, spec := range PhysicalDiskMetrics {
		if spec.Name == name {
			return spec, true
		}
	}
	return MetricSpec{}, false
}

// WorstState returns the more severe of two hw.state values; an unknown value counts as the least severe.
func WorstState(a, b string) string {
	if stateRank(b) > stateRank(a) {
		return b
	}
	return a
}

func stateRank(state string) int {
	for i, s := range States {
		if s == state {
			return i
		}
	}
	return -1
}
