// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package hwschema

// Metric names of the BMC receivers. The hw.* names follow the OpenTelemetry
// semantic conventions; hw.bmc.up and hw.bmc.scrape.duration are ours. The
// names the disk receivers share (hw.status, hw.temperature, the physical disk
// metrics) are in schema.go.
const (
	MetricTemperatureLimit       = "hw.temperature.limit"
	MetricFanSpeed               = "hw.fan.speed"
	MetricFanSpeedRatio          = "hw.fan.speed_ratio"
	MetricVoltage                = "hw.voltage"
	MetricHostPower              = "hw.host.power"
	MetricPowerSupplyUsage       = "hw.power_supply.usage"
	MetricPowerSupplyLimit       = "hw.power_supply.limit"
	MetricPowerSupplyUtilization = "hw.power_supply.utilization"
	MetricMemorySize             = "hw.memory.size"
	MetricBMCUp                  = "hw.bmc.up"
	MetricBMCScrapeDuration      = "hw.bmc.scrape.duration"
)

// Units of the BMC metrics.
const (
	UnitRPM   = "rpm"
	UnitVolts = "V"
	UnitWatts = "W"
)

// Attribute keys of the BMC receivers.
const (
	AttrLimitType = "hw.limit_type"
	AttrReason    = "reason"

	ResHostName = "host.name"
	ResAddress  = "server.address"
	ResProtocol = "hw.bmc.protocol"

	ProtocolIPMI    = "ipmi"
	ProtocolRedfish = "redfish"
)

// Values of hw.type for the components a BMC reports. TypePhysicalDisk is in
// schema.go; TypeBMC is ours, the conventions have no value for the controller.
const (
	TypeHost        = "host"
	TypeEnclosure   = "enclosure"
	TypeFan         = "fan"
	TypePowerSupply = "power_supply"
	TypeMemory      = "memory"
	TypeCPU         = "cpu"
	TypeBMC         = "bmc"
)

// DiskStateUsed is hw.physical_disk.state on the endurance metric when the
// value is the fraction of the life used.
const DiskStateUsed = "used"

// Values of hw.limit_type.
const (
	LimitMax          = "max"
	LimitCritical     = "critical"
	LimitHighDegraded = "high.degraded"
)

// Values of the reason attribute of hw.bmc.up.
const (
	ReasonTimeout = "timeout"
	ReasonAuth    = "auth"
	ReasonRefused = "refused"
	ReasonBackoff = "backoff"
	ReasonTLS     = "tls"
)

// BMCMetricNames lists every metric name the BMC receivers emit; a source check
// uses it to find a name written outside this module.
func BMCMetricNames() []string {
	return []string{
		MetricTemperature, MetricTemperatureLimit, MetricFanSpeed, MetricFanSpeedRatio, MetricVoltage,
		MetricHostPower, MetricPowerSupplyUsage, MetricPowerSupplyLimit, MetricPowerSupplyUtilization,
		MetricPhysicalDiskSize, MetricPhysicalDiskEndurance, MetricMemorySize, MetricStatus,
		MetricBMCUp, MetricBMCScrapeDuration,
	}
}

// StateFromHealth maps a Redfish Status.Health string to an hw.state value. A
// health the map does not know gives false, and the caller emits no hw.status.
func StateFromHealth(health string) (string, bool) {
	switch health {
	case "OK", "ok":
		return StateOK, true
	case "Warning":
		return StateDegraded, true
	case "Critical":
		return StateFailed, true
	}
	return "", false
}
