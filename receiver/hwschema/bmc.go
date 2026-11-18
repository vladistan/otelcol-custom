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
	MetricBMCScrapeErrors        = "hw.bmc.scrape.errors"

	// Storage series of a BMC. The names under hw.disk_controller, hw.logical_disk and the drive indicator
	// are ours; the conventions have none for them.
	MetricDiskControllerSpeed   = "hw.disk_controller.speed"
	MetricLogicalDiskLimit      = "hw.logical_disk.limit"
	MetricPhysicalDiskIndicator = "hw.physical_disk.indicator"

	// MetricPhysicalDiskPredictedFailure is 1 when the BMC predicts the drive will fail, whatever its health.
	MetricPhysicalDiskPredictedFailure = "hw.physical_disk.predicted_failure"
	// MetricLogicalDiskMediaSpanCount is the number of spans of a volume.
	MetricLogicalDiskMediaSpanCount = "hw.logical_disk.media_span_count"
	// MetricStorageDriveCount is the number of drives a storage resource lists.
	MetricStorageDriveCount = "hw.storage.drive_count"
)

// Units of the BMC metrics.
const (
	UnitRPM   = "rpm"
	UnitVolts = "V"
	UnitWatts = "W"

	UnitMegabitPerSecond = "Mbit/s"
	UnitSpans            = "{span}"
	UnitDrives           = "{drive}"
)

// Attribute keys of the BMC receivers.
const (
	AttrLimitType = "hw.limit_type"
	AttrReason    = "reason"

	// AttrPathGroup names the group of Redfish paths a failed read belongs to.
	AttrPathGroup = "path_group"

	// Attributes of drives and volumes.
	AttrPhysicalDiskProtocol = "hw.physical_disk.protocol"
	AttrRAIDLevel            = "hw.logical_disk.raid_level"
	AttrVolumeType           = "hw.logical_disk.volume_type"

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

	// Storage: the rollup of one storage resource, its controller and its volumes.
	TypeStorage        = "storage"
	TypeDiskController = "disk_controller"
	TypeLogicalDisk    = "logical_disk"
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

// Inventory series the ipmi receiver emits from the BMC FRU: an info gauge of
// value 1 per record, and the status of the read (1 read, 0 unreadable).
const (
	MetricBMCFRUInfo      = "inventory.bmc.fru.info"
	MetricInventoryStatus = "inventory.collection.status"

	AttrFRUType         = "fru.type"
	AttrInventorySource = "inventory.source"
	AttrPartNumber      = "hw.part_number"
	AttrAssetTag        = "hw.asset_tag"
	AttrVersion         = "hw.version"

	SourceFRU = "fru"
)

// Values of fru.type.
const (
	FRUBoard   = "board"
	FRUProduct = "product"
	FRUChassis = "chassis"
	FRUDIMM    = "dimm"
	FRUCPU     = "cpu"
)

// Values of the reason attribute of hw.bmc.up.
const (
	ReasonTimeout = "timeout"
	ReasonAuth    = "auth"
	ReasonRefused = "refused"
	ReasonBackoff = "backoff"
	ReasonTLS     = "tls"
	// ReasonError is a read that stopped on an internal error.
	ReasonError = "error"
)

// BMCMetricNames lists every metric name the BMC receivers emit; a source check
// uses it to find a name written outside this module.
func BMCMetricNames() []string {
	return []string{
		MetricTemperature, MetricTemperatureLimit, MetricFanSpeed, MetricFanSpeedRatio, MetricVoltage,
		MetricHostPower, MetricPowerSupplyUsage, MetricPowerSupplyLimit, MetricPowerSupplyUtilization,
		MetricPhysicalDiskSize, MetricPhysicalDiskEndurance, MetricMemorySize, MetricStatus,
		MetricBMCUp, MetricBMCScrapeDuration, MetricBMCScrapeErrors,
		MetricDiskControllerSpeed, MetricLogicalDiskLimit, MetricPhysicalDiskIndicator,
		MetricPhysicalDiskPredictedFailure, MetricLogicalDiskMediaSpanCount, MetricStorageDriveCount,
		MetricBMCFRUInfo, MetricInventoryStatus,
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
