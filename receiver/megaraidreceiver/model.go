// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package megaraidreceiver

import (
	"fmt"
	"strings"

	"github.com/vladistan/otelcol-custom/receiver/hwschema"
	"github.com/vladistan/otelcol-custom/receiver/smartdecode"
)

// Values of raid.smart_state.
const (
	smartOK          = "ok"
	smartPredicted   = "predicted_failure"
	smartOff         = "disabled"
	smartUnsupported = "unsupported"
	smartStandby     = "standby"
	smartNotATA      = "not_ata"
	smartUnavailable = "unavailable"
	smartAttrNorm    = "normalized"
	smartAttrRaw     = "raw"
	attrPowerOnHours = 9
	attrPowerCycles  = 12
	secondsPerHour   = 3600
	sensorROC        = "roc"
	sensorBoard      = "controller"
	sensorDrive      = "drive"
	sensorBattery    = "battery"
	pdUnknownState   = ""
	levelUnknown     = "unknown"
	stripeLog2Max    = 20
	bytesPerSector   = 512
)

// pdRecord is one physical drive: its list entry, its info and, when SMART ran, the result.
type pdRecord struct {
	addr  pdAddr
	info  pdInfo
	smart *smartResult
}

// smartResult is what one SMART read of a drive gave.
type smartResult struct {
	state string
	data  *smartdecode.ATAData
}

// snapshot is everything one scrape read from one controller.
type snapshot struct {
	ctrl controller
	info ctrlInfo
	lds  []ldInfo
	pds  []pdRecord
	bbu  *bbuInfo
}

func (p pdRecord) id(c controller) string {
	if p.info.Serial != "" {
		return p.info.Serial
	}
	return fmt.Sprintf("%s/pd%d", c.id(), p.addr.DeviceID)
}

func (p pdRecord) name() string {
	return strings.TrimSpace(p.info.Vendor + " " + p.info.Product)
}

// controllerState maps the counts of the controller info to a state.
func controllerState(c ctrlInfo) string {
	switch {
	case c.LDOffline > 0 || c.PDFailed > 0:
		return hwschema.StateFailed
	case c.LDDegraded > 0 || c.PDPredFail > 0:
		return hwschema.StateDegraded
	}
	return hwschema.StateOK
}

// ldState maps a logical drive state; ok is false for a value the table does not know.
func ldState(state uint8) (string, bool) {
	switch state {
	case ldStateOptimal:
		return hwschema.StateOK, true
	case ldStatePartial, ldStateDegraded:
		return hwschema.StateDegraded, true
	case ldStateOffline:
		return hwschema.StateFailed, true
	}
	return pdUnknownState, false
}

// pdState maps a drive firmware state and its predictive-failure count.
func pdState(fw uint16, predFail uint32) (string, bool) {
	var st string
	switch fw {
	case pdUnconfiguredGood, pdHotSpare, pdOnline, pdSystem:
		st = hwschema.StateOK
	case pdRebuild, pdCopyback:
		st = hwschema.StateDegraded
	case pdUnconfiguredBad, pdOffline, pdFailed:
		st = hwschema.StateFailed
	default:
		return pdUnknownState, false
	}
	if st == hwschema.StateOK && predFail > 0 {
		st = hwschema.StatePredictedFailure
	}
	return st, true
}

// bbuState maps the battery status bits.
func bbuState(status uint32) string {
	switch {
	case status&(bbuPackMissing|bbuI2CError) != 0:
		return hwschema.StateFailed
	case status&(bbuVoltageLow|bbuTempHigh|bbuLearnFailed|bbuLearnTimeout|bbuReplace) != 0:
		return hwschema.StateDegraded
	}
	return hwschema.StateOK
}

// raidLevel names the RAID level of a logical drive from the primary level and the span depth.
func raidLevel(primary uint8, spans int) string {
	switch {
	case primary == 0:
		return "raid0"
	case primary == 1 && spans > 1:
		return "raid10"
	case primary == 1:
		return "raid1"
	case primary == 5 && spans > 1:
		return "raid50"
	case primary == 5:
		return "raid5"
	case primary == 6 && spans > 1:
		return "raid60"
	case primary == 6:
		return "raid6"
	}
	return levelUnknown
}

// stripBytes converts the log2 strip size, in 512 byte sectors, to bytes; 0 when it is out of range.
func stripBytes(log2 uint8) uint64 {
	if log2 > stripeLog2Max {
		return 0
	}
	return uint64(bytesPerSector) << log2
}

// emit adds the metrics of the snapshot. warn is called once for each value the tables do not know.
func (s *snapshot) emit(b *builder, warn func(msg string)) {
	cid := ident{id: s.ctrl.id(), name: s.info.Product}
	b.status(cid, typeController, controllerState(s.info))
	b.gauge(metricCtrlInfo, ident{id: cid.id, name: cid.name, extra: []string{
		hwschema.AttrModel, s.info.Product, hwschema.AttrSerialNumber, s.info.Serial, hwschema.AttrFirmwareVersion, s.info.Firmware,
		attrMemory, itoa(s.info.MemoryMB),
	}}, typeController, 1)
	if s.info.TempROC > 0 {
		b.gauge(metricTemperature, cid, typeController, float64(s.info.TempROC), attrLocation, sensorROC)
	}
	if s.info.TempBoard > 0 {
		b.gauge(metricTemperature, cid, typeController, float64(s.info.TempBoard), attrLocation, sensorBoard)
	}

	for _, l := range s.lds {
		s.emitLD(b, cid.id, l, warn)
	}
	for _, p := range s.pds {
		s.emitPD(b, cid.id, p, warn)
	}
	if s.bbu != nil {
		s.emitBBU(b, cid.id, *s.bbu)
	}
}

func (s *snapshot) emitLD(b *builder, parent string, l ldInfo, warn func(string)) {
	id := ident{id: fmt.Sprintf("%s/ld%d", parent, l.Target), name: l.Name, parent: parent}
	if st, ok := ldState(l.State); ok {
		b.status(id, typeLogicalDisk, st)
	} else {
		warn(fmt.Sprintf("logical drive %d has the unknown state %d", l.Target, l.State))
	}
	b.gauge(metricLDLevel, ident{id: id.id, name: id.name, parent: parent, extra: []string{attrLevel, raidLevel(l.Level, l.SpanDepth)}}, typeLogicalDisk, 1)
	if strip := stripBytes(l.StripeLog2); strip > 0 {
		b.gauge(metricLDStrip, id, typeLogicalDisk, float64(strip))
	}
	b.gauge(metricLDCache, ident{id: id.id, name: id.name, parent: parent, extra: []string{attrCache, fmt.Sprintf("0x%02x", l.Cache)}}, typeLogicalDisk, float64(l.Cache))
	if l.SizeBytes > 0 {
		b.gauge(metricLDLimit, id, typeLogicalDisk, float64(l.SizeBytes))
	}
	for op, v := range l.Progress {
		b.gauge(metricProgress, id, typeLogicalDisk, v, attrOperation, op)
	}
}

func (s *snapshot) emitPD(b *builder, parent string, p pdRecord, warn func(string)) {
	id := ident{id: p.id(s.ctrl), name: p.name(), parent: parent,
		extra: []string{attrEnclosure, itoa(int(p.info.EnclID)), attrSlot, itoa(int(p.info.Slot))}}
	state, ok := pdState(p.info.FWState, p.info.PredFail)
	if ok && p.smart != nil && p.smart.state == smartPredicted {
		state = hwschema.WorstState(state, hwschema.StatePredictedFailure)
	}
	if ok {
		b.status(id, typePhysicalDisk, state)
	} else {
		warn(fmt.Sprintf("drive %d has the unknown firmware state %#x", p.addr.DeviceID, p.info.FWState))
	}
	if p.info.Temperature > 0 {
		b.gauge(metricTemperature, id, typePhysicalDisk, float64(p.info.Temperature), attrLocation, sensorDrive)
	}
	b.count(metricErrors, id, typePhysicalDisk, uint64(p.info.MediaErrors), hwschema.AttrErrorType, hwschema.ErrorMedia)
	b.count(metricErrors, id, typePhysicalDisk, uint64(p.info.OtherErrors), hwschema.AttrErrorType, errorOther)
	b.count(metricErrors, id, typePhysicalDisk, uint64(p.info.PredFail), hwschema.AttrErrorType, errorPredictive)
	if p.info.SizeBytes > 0 {
		b.gauge(metricPDSize, id, typePhysicalDisk, float64(p.info.SizeBytes))
	}
	for op, v := range p.info.Progress {
		b.gauge(metricProgress, id, typePhysicalDisk, v, attrOperation, op)
	}
	if p.smart != nil {
		emitSmart(b, id, p.smart)
	}
}

func emitSmart(b *builder, id ident, r *smartResult) {
	b.gauge(metricSmartState, ident{id: id.id, name: id.name, parent: id.parent, extra: []string{attrSmartState, r.state}}, typePhysicalDisk, 1)
	if r.data == nil {
		return
	}
	want := map[uint8]bool{}
	for _, a := range smartdecode.DefaultAttributeIDs() {
		want[a] = true
	}
	for _, a := range r.data.Attributes {
		if !want[a.ID] {
			continue
		}
		attrID := itoa(int(a.ID))
		b.gauge(hwschema.MetricPhysicalDiskSmart, id, typePhysicalDisk, float64(a.Value), attrAttrID, attrID, attrValueType, smartAttrNorm)
		b.gauge(hwschema.MetricPhysicalDiskSmart, id, typePhysicalDisk, float64(a.Raw), attrAttrID, attrID, attrValueType, smartAttrRaw)
		switch a.ID {
		case attrPowerOnHours:
			b.gauge(hwschema.MetricPhysicalDiskPowerOnTime, id, typePhysicalDisk, float64(a.Raw)*secondsPerHour)
		case attrPowerCycles:
			b.count(hwschema.MetricPhysicalDiskPowerCycles, id, typePhysicalDisk, a.Raw)
		}
	}
}

func (s *snapshot) emitBBU(b *builder, parent string, bb bbuInfo) {
	id := ident{id: parent + "/bbu", parent: parent}
	b.status(id, typeBattery, bbuState(bb.Status))
	if bb.hasCapacity {
		b.gauge(metricBatteryCharge, id, typeBattery, min(max(float64(bb.ChargePct)/100, 0), 1))
		b.gauge(metricBatteryCycles, id, typeBattery, float64(bb.CycleCount))
		if bb.RunTimeMin > 0 && bb.RunTimeMin != 0xFFFF {
			b.gauge(metricBatteryTimeLeft, id, typeBattery, float64(bb.RunTimeMin)*60)
		}
	}
	if bb.Temperature > 0 {
		b.gauge(metricTemperature, id, typeBattery, float64(bb.Temperature), attrLocation, sensorBattery)
	}
	if bb.hasNextLearn {
		b.gauge(metricBatteryLearn, id, typeBattery, float64(int64(bb.NextLearn)+mfiEpoch))
	}
}
