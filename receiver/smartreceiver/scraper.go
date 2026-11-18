// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package smartreceiver

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"

	"github.com/vladistan/otelcol-custom/receiver/hwschema"
	"github.com/vladistan/otelcol-custom/receiver/smartdecode"
)

// The ATA SMART attribute ids the receiver reads by number, as vendors define them.
const (
	attrReallocated        uint8 = 5
	attrPowerOnHours       uint8 = 9
	attrPowerCycles        uint8 = 12
	attrAirflowTemperature uint8 = 190
	attrTemperature        uint8 = 194
	attrPendingSectors     uint8 = 197
	attrUncorrectable      uint8 = 198
	attrCRCErrors          uint8 = 199
	attrLifetimeRemaining  uint8 = 202
	attrSSDLifeLeft        uint8 = 231
	attrWearout            uint8 = 233
	attrTotalWritten       uint8 = 241
	attrTotalRead          uint8 = 242
)

type scraper struct {
	logger           *zap.Logger
	bus              bus
	sel              selector
	attrs            attributeSelection
	valueTypes       []string
	isStandbySkipped bool
	maxDevices       int
	timeout          time.Duration
	skipped          metric.Int64Counter
	start            pcommon.Timestamp

	// ioUnit gives the bytes in one unit of an attribute that counts data moved.
	ioUnit func(id uint8) (uint64, bool)

	episodes     map[string]string // device to the reason of its failure, set while the failure lasts
	hasWarnedCap bool
	listProblem  string // text of the listing failure being reported, empty when the last listing was whole
}

func newScraper(logger *zap.Logger, cfg *Config, b bus, skipped metric.Int64Counter, start time.Time) (*scraper, error) {
	sel, err := newSelector(cfg.Include, cfg.Exclude)
	if err != nil {
		return nil, err
	}
	attrs, err := cfg.attributeSelection()
	if err != nil {
		return nil, err
	}
	return &scraper{
		logger:           logger,
		bus:              b,
		sel:              sel,
		attrs:            attrs,
		valueTypes:       cfg.ValueTypes,
		isStandbySkipped: cfg.SkipIfStandby,
		maxDevices:       cfg.MaxDevices,
		timeout:          cfg.CommandTimeout,
		skipped:          skipped,
		start:            pcommon.NewTimestampFromTime(start),
		ioUnit:           smartdecode.IOUnitBytes,
		episodes:         map[string]string{},
	}, nil
}

// scrape reads the selected devices one at a time. A device that fails is skipped for this interval.
func (s *scraper) scrape(ctx context.Context, now time.Time) pmetric.Metrics {
	md := pmetric.NewMetrics()
	devs, err := s.bus.discover()
	if err != nil && !errors.Is(err, errPartialList) {
		s.reportListProblem("Failed to list the disks", err)
		return md
	}
	// A partial list still carries the disks found; the problem is logged once while it lasts.
	if err != nil {
		s.reportListProblem("The disk list is incomplete; the disks found are still read", err)
	} else {
		s.listProblem = ""
	}
	devs = s.sel.filter(devs)
	if len(devs) > s.maxDevices {
		if !s.hasWarnedCap {
			s.hasWarnedCap = true
			s.logger.Warn("More devices than max_devices; the rest are not read",
				zap.Int("devices", len(devs)), zap.Int("max_devices", s.maxDevices))
		}
		devs = devs[:s.maxDevices]
	} else {
		s.hasWarnedCap = false
	}

	b := newBuilder(md, s.start, pcommon.NewTimestampFromTime(now))
	for _, d := range devs {
		if ctx.Err() != nil {
			break // shutdown: finish the device in hand and read no more
		}
		s.read(ctx, b, d)
	}
	return md
}

// reportListProblem warns when the listing failure differs from the one last reported, so a failure that
// repeats every interval gives one line until it changes or ends.
func (s *scraper) reportListProblem(message string, err error) {
	if text := err.Error(); text != s.listProblem {
		s.listProblem = text
		s.logger.Warn(message, zap.Error(err))
	}
}

func (s *scraper) read(ctx context.Context, b *builder, d device) {
	var err error
	switch d.kind {
	case kindNVMe:
		err = s.readNVMe(b, d)
	default:
		err = s.readATA(b, d)
	}
	if err != nil {
		s.skip(ctx, d, err)
		return
	}
	delete(s.episodes, d.name)
}

func (s *scraper) readATA(b *builder, d device) error {
	tr, closer, err := s.bus.openATA(d.name, s.timeout)
	if err != nil {
		return err
	}
	defer func() { _ = closer.Close() }()
	data, err := smartdecode.ReadATA(tr, smartdecode.ATAOptions{SkipIfStandby: s.isStandbySkipped})
	if err != nil {
		return err
	}
	s.emitATA(b, d, data)
	return nil
}

func (s *scraper) readNVMe(b *builder, d device) error {
	tr, closer, err := s.bus.openNVMe(d.name, s.timeout)
	if err != nil {
		return err
	}
	defer func() { _ = closer.Close() }()
	data, err := smartdecode.ReadNVMe(tr)
	if err != nil {
		return err
	}
	s.emitNVMe(b, d, data)
	return nil
}

// skip counts a skipped device by reason and logs the failure once while it lasts.
func (s *scraper) skip(ctx context.Context, d device, err error) {
	reason := reasonError
	switch {
	case errors.Is(err, smartdecode.ErrPermission):
		reason = reasonPermission
	case errors.Is(err, smartdecode.ErrTimeout):
		reason = reasonTimeout
	case errors.Is(err, smartdecode.ErrUnsupported), errors.Is(err, smartdecode.ErrSMARTUnsupported):
		reason = reasonUnsupported
	case errors.Is(err, smartdecode.ErrSMARTDisabled):
		reason = reasonSmartDisabled
	case errors.Is(err, smartdecode.ErrStandby):
		reason = reasonStandby
	}
	s.skipped.Add(ctx, 1, metric.WithAttributes(attribute.String(reasonLabelKey, reason)))

	// An episode is one device failing for one reason; a change of reason starts a new one.
	first := s.episodes[d.name] != reason
	s.episodes[d.name] = reason
	switch reason {
	case reasonPermission:
		if !first {
			return
		}
		if errors.Is(err, smartdecode.ErrFileAccess) {
			s.logger.Error("Cannot open the device file; run the collector as root, or give its user read access to the file",
				zap.String("device", d.name), zap.String("needs", "read access to the device file"), zap.Error(err))
			return
		}
		s.logger.Error("The kernel refused the command; the collector's user lacks the capability named here",
			zap.String("device", d.name), zap.String("missing_capability", capabilityFor(d)), zap.Error(err))
	case reasonError, reasonTimeout:
		if first {
			s.logger.Warn("Skipped the device for this interval", zap.String("device", d.name), zap.String("reason", reason), zap.Error(err))
		}
	case reasonUnsupported, reasonSmartDisabled:
		if first {
			s.logger.Info("Skipped the device; it gives no SMART data on this path", zap.String("device", d.name), zap.String("reason", reason), zap.Error(err))
		}
	default:
		s.logger.Debug("Skipped the device", zap.String("device", d.name), zap.String("reason", reason), zap.Error(err))
	}
}

// capabilityFor names the capability a device kind needs: NVMe admin commands need CAP_SYS_ADMIN and
// ATA pass-through needs CAP_SYS_RAWIO.
func capabilityFor(d device) string {
	if d.kind == kindNVMe {
		return "CAP_SYS_ADMIN"
	}
	return "CAP_SYS_RAWIO"
}

func (s *scraper) emitNVMe(b *builder, d device, data smartdecode.NVMeData) {
	id := ident{
		id: firstNonEmpty(data.Identify.Serial, d.name), name: d.name, model: data.Identify.Model,
		serial: data.Identify.Serial, firmware: data.Identify.Firmware, diskType: hwschema.DiskNVMe,
		vendor: smartdecode.NVMeVendor(data.Identify.VendorID),
	}
	smart := data.Smart
	// TNVMCAP is 0 on many consumer drives; the namespaces then give the size.
	if size := firstNonZero(data.Identify.SizeBytes, d.nsBytes); size > 0 {
		b.count(hwschema.MetricPhysicalDiskSize, id, size)
	}
	b.status(id, smartdecode.NVMeState(smart.CriticalWarning))
	if smart.HasComposite {
		b.gauge(hwschema.MetricTemperature, id, float64(smart.CompositeCelsius), hwschema.AttrSensorLocation, "composite")
	}
	for _, sensor := range smart.SensorsCelsius {
		b.gauge(hwschema.MetricTemperature, id, float64(sensor.Celsius), hwschema.AttrSensorLocation, "sensor"+strconv.Itoa(sensor.Index))
	}
	// The log page allows a value above 100 for a drive past its rated life; the ratio stops at 0.
	used := min(smart.PercentageUsed, 100)
	b.gauge(hwschema.MetricPhysicalDiskEndurance, id, 1-float64(used)/100, hwschema.AttrPhysicalDiskState, hwschema.DiskStateRemaining)
	b.count(hwschema.MetricErrors, id, smart.MediaErrors, hwschema.AttrErrorType, hwschema.ErrorMedia)
	b.gauge(hwschema.MetricPhysicalDiskPowerOnTime, id, float64(smart.PowerOnHours)*3600)
	b.count(hwschema.MetricPhysicalDiskPowerCycles, id, smart.PowerCycles)
	b.count(hwschema.MetricPhysicalDiskIO, id, smart.BytesRead(), hwschema.AttrDiskIODir, hwschema.DirectionRead)
	b.count(hwschema.MetricPhysicalDiskIO, id, smart.BytesWritten(), hwschema.AttrDiskIODir, hwschema.DirectionWrite)
	if s.wantsValue(hwschema.ValueRaw) {
		for name, v := range map[string]uint64{
			"Available_Spare":           uint64(smart.AvailableSpare),
			"Available_Spare_Threshold": uint64(smart.SpareThreshold),
			"Unsafe_Shutdowns":          smart.UnsafeShutdowns,
		} {
			b.count(hwschema.MetricPhysicalDiskSmart, id, v,
				hwschema.AttrSmartAttribute, name, hwschema.AttrSmartValueType, hwschema.ValueRaw)
		}
	}
}

func (s *scraper) wantsValue(kind string) bool {
	for _, v := range s.valueTypes {
		if v == kind {
			return true
		}
	}
	return false
}

func findAttr(attrs []smartdecode.Attribute, id uint8) (smartdecode.Attribute, bool) {
	for _, a := range attrs {
		if a.ID == id {
			return a, true
		}
	}
	return smartdecode.Attribute{}, false
}

func (s *scraper) emitATA(b *builder, d device, data smartdecode.ATAData) {
	info := data.Identify
	diskType := hwschema.DiskHDD
	if info.IsSSD() {
		diskType = hwschema.DiskSSD
	}
	id := ataIdent(d, info, diskType)

	if info.SizeBytes > 0 {
		b.count(hwschema.MetricPhysicalDiskSize, id, info.SizeBytes)
	}
	switch data.Health {
	case smartdecode.HealthOK:
		b.status(id, hwschema.StateOK)
	case smartdecode.HealthPredictedFailure:
		b.status(id, hwschema.StatePredictedFailure)
	}

	if temp, ok := ataTemperature(data); ok {
		b.gauge(hwschema.MetricTemperature, id, temp)
	}
	if info.IsSSD() {
		for _, endurance := range []uint8{attrSSDLifeLeft, attrWearout, attrLifetimeRemaining} {
			if a, ok := findAttr(data.Attributes, endurance); ok {
				b.gauge(hwschema.MetricPhysicalDiskEndurance, id, float64(a.Value)/100, hwschema.AttrPhysicalDiskState, hwschema.DiskStateRemaining)
				break
			}
		}
	}
	for _, e := range []struct {
		id   uint8
		kind string
	}{
		{attrReallocated, hwschema.ErrorBadSector}, {attrPendingSectors, hwschema.ErrorBadSector},
		{attrUncorrectable, hwschema.ErrorBadSector}, {attrCRCErrors, hwschema.ErrorCRC},
	} {
		if a, ok := findAttr(data.Attributes, e.id); ok {
			b.count(hwschema.MetricErrors, id, a.Raw, hwschema.AttrErrorType, e.kind, hwschema.AttrSmartAttribute, smartdecode.AttributeName(e.id))
		}
	}
	if a, ok := findAttr(data.Attributes, attrPowerOnHours); ok {
		b.gauge(hwschema.MetricPhysicalDiskPowerOnTime, id, float64(a.Raw&0xFFFFFFFF)*3600)
	}
	if a, ok := findAttr(data.Attributes, attrPowerCycles); ok {
		b.count(hwschema.MetricPhysicalDiskPowerCycles, id, a.Raw)
	}
	for _, m := range []struct {
		id  uint8
		dir string
	}{{attrTotalWritten, hwschema.DirectionWrite}, {attrTotalRead, hwschema.DirectionRead}} {
		a, ok := findAttr(data.Attributes, m.id)
		if !ok {
			continue
		}
		if unit, named := s.ioUnit(m.id); named {
			b.count(hwschema.MetricPhysicalDiskIO, id, a.Raw*unit, hwschema.AttrDiskIODir, m.dir)
		}
	}
	for _, a := range data.Attributes {
		if s.attrs.wants(a.ID) {
			s.emitAttribute(b, id, a)
		}
	}
}

// ataIdent builds the identity of a SATA disk from its IDENTIFY data.
func ataIdent(d device, i smartdecode.Identify, diskType string) ident {
	return ident{
		id: firstNonEmpty(i.Serial, i.WWN, d.name), name: d.name, model: i.Model,
		serial: i.Serial, firmware: i.Firmware, diskType: diskType, vendor: smartdecode.ATAVendor(i.Model),
	}
}

// ataTemperature takes the low byte of attribute 194, else 190, else the Device Statistics value.
func ataTemperature(data smartdecode.ATAData) (float64, bool) {
	for _, id := range []uint8{attrTemperature, attrAirflowTemperature} {
		if a, ok := findAttr(data.Attributes, id); ok {
			return float64(a.Raw & 0xFF), true
		}
	}
	if data.HasStatsTemperature {
		return float64(data.StatsTemperature), true
	}
	return 0, false
}

func (s *scraper) emitAttribute(b *builder, id ident, a smartdecode.Attribute) {
	name := smartdecode.AttributeName(a.ID)
	for _, vt := range s.valueTypes {
		var v uint64
		switch vt {
		case hwschema.ValueNormalized:
			v = uint64(a.Value)
		case hwschema.ValueWorst:
			v = uint64(a.Worst)
		case hwschema.ValueThreshold:
			if !a.HasThreshold {
				continue
			}
			v = uint64(a.Threshold)
		case hwschema.ValueRaw:
			v = a.Raw
		default:
			panic(fmt.Sprintf("smartreceiver: unknown value type %q", vt))
		}
		b.gauge(hwschema.MetricPhysicalDiskSmart, id, float64(v), hwschema.AttrSmartAttribute, name, hwschema.AttrSmartValueType, vt)
	}
}
