// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package inventoryreceiver

import (
	"context"
	"sort"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
)

const scopeName = "github.com/vladistan/otelcol-custom/receiver/inventoryreceiver"

const (
	metricCPU    = "inventory.cpu.info"
	metricMemory = "inventory.memory_module.info"
	metricBoard  = "inventory.board.info"
	metricBIOS   = "inventory.bios.info"
	metricSystem = "inventory.system.info"
	metricDrive  = "inventory.drive.info"
	metricGPU    = "inventory.gpu.info"
	metricStatus = "inventory.collection.status"

	// metricMemoryTotal is one series per host: the physical RAM, not a module.
	metricMemoryTotal = "inventory.memory.info"
)

const (
	attrID            = "hw.id"
	attrVendor        = "hw.vendor"
	attrModel         = "hw.model"
	attrSerial        = "hw.serial_number"
	attrAssetTag      = "hw.asset_tag"
	attrVersion       = "hw.version"
	attrFirmware      = "hw.firmware_version"
	attrWWID          = "hw.wwid"
	attrDriver        = "hw.driver"
	attrDriverVersion = "hw.driver_version"
	attrSource        = "inventory.source"

	attrCPUSocket  = "cpu.socket"
	attrCPUSockets = "cpu.sockets"
	attrCPUCores   = "cpu.cores"
	attrCPUThreads = "cpu.threads"
	attrCPUNominal = "cpu.frequency.nominal"
	attrCPUMax     = "cpu.frequency.max"

	attrMemSize       = "memory.size"
	attrMemType       = "memory.type"
	attrMemSpeed      = "memory.speed"
	attrMemConfigured = "memory.configured_speed"
	attrMemForm       = "memory.form_factor"
	attrMemBank       = "memory.bank"
	attrMemTotal      = "memory.total"

	attrBIOSDate        = "bios.date"
	attrSystemFamily    = "system.family"
	attrChassisType     = "chassis.type"
	attrChassisSerial   = "chassis.serial_number"
	attrChassisAssetTag = "chassis.asset_tag"
	attrChassisVendor   = "chassis.vendor"

	attrDriveSize = "drive.size"
	attrDriveType = "drive.type"
	attrDriveBus  = "drive.bus"

	attrGPUVendorID = "gpu.vendor_id"
	attrGPUDeviceID = "gpu.device_id"
	attrGPUMemory   = "gpu.memory"
)

// Sources named by the status series.
const (
	srcCPU    = "cpu"
	srcMemory = "memory"
	srcBoard  = "board"
	srcBIOS   = "bios"
	srcSystem = "system"
	srcDrive  = "drive"
	srcGPU    = "gpu"

	// srcMemoryTotal is the total RAM from /proc/meminfo or the Windows memory call; it does not use SMBIOS.
	srcMemoryTotal = "memory_total"
)

var allSources = []string{srcCPU, srcMemory, srcMemoryTotal, srcBoard, srcBIOS, srcSystem, srcDrive, srcGPU}

type hostKind int

const (
	hostPhysical hostKind = iota
	hostVirtual
	hostContainer
	hostUndecided
)

// fact is one info series: values are string or int64.
type fact struct {
	metric string
	attrs  map[string]any
}

func newFact(metric string) fact { return fact{metric: metric, attrs: map[string]any{}} }

// setStr adds a text attribute unless the cleaned value is empty.
func (f fact) setStr(key, value string) {
	if v := cleanValue(value); v != "" {
		f.attrs[key] = v
	}
}

// setNum adds a number attribute when it is above zero.
func (f fact) setNum(key string, value int64) {
	if value > 0 {
		f.attrs[key] = value
	}
}

// hasData tells whether the fact holds anything besides its identity.
func (f fact) hasData() bool {
	for k := range f.attrs {
		if k != attrID {
			return true
		}
	}
	return false
}

type inventory struct {
	host          hostKind
	hostReason    string
	facts         []fact
	status        map[string]bool // source to read or not
	dmiUnreadable bool
	drivesCapped  bool
	// notes name facts the host could not give; they carry no identifiers and are logged at debug level.
	notes []string
}

func newInventory() inventory { return inventory{status: map[string]bool{}} }

type source interface {
	read(ctx context.Context) inventory
}

// buildMetrics turns a collection into one resource of info gauges and status gauges.
func buildMetrics(inv inventory, now time.Time) pmetric.Metrics {
	md := pmetric.NewMetrics()
	sm := md.ResourceMetrics().AppendEmpty().ScopeMetrics().AppendEmpty()
	sm.Scope().SetName(scopeName)
	ts := pcommon.NewTimestampFromTime(now)
	gauges := map[string]pmetric.Gauge{}
	gauge := func(name string) pmetric.Gauge {
		if g, ok := gauges[name]; ok {
			return g
		}
		m := sm.Metrics().AppendEmpty()
		m.SetName(name)
		m.SetUnit("1")
		g := m.SetEmptyGauge()
		gauges[name] = g
		return g
	}
	for _, f := range inv.facts {
		dp := gauge(f.metric).DataPoints().AppendEmpty()
		dp.SetTimestamp(ts)
		dp.SetIntValue(1)
		keys := make([]string, 0, len(f.attrs))
		for k := range f.attrs {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			switch v := f.attrs[k].(type) {
			case string:
				dp.Attributes().PutStr(k, v)
			case int64:
				dp.Attributes().PutInt(k, v)
			}
		}
	}
	for _, src := range allSources {
		read, known := inv.status[src]
		if !known {
			continue
		}
		dp := gauge(metricStatus).DataPoints().AppendEmpty()
		dp.SetTimestamp(ts)
		dp.Attributes().PutStr(attrSource, src)
		if read {
			dp.SetIntValue(1)
		} else {
			dp.SetIntValue(0)
		}
	}
	return md
}
