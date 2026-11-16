// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package smartreceiver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/vladistan/otelcol-custom/receiver/smartdecode"
	"github.com/vladistan/otelcol-custom/receiver/smartdecode/smarttest"
)

// fakeBus lists devices and hands out fake transports.
type fakeBus struct {
	devs    []device
	ata     map[string]smartdecode.ATATransport
	nvme    map[string]smartdecode.NVMeTransport
	openErr map[string]error
	opened  []string
}

func (b *fakeBus) discover() ([]device, error) { return b.devs, nil }

func (b *fakeBus) openATA(name string, _ time.Duration) (smartdecode.ATATransport, io.Closer, error) {
	b.opened = append(b.opened, name)
	if err := b.openErr[name]; err != nil {
		return nil, nil, err
	}
	return b.ata[name], io.NopCloser(nil), nil
}

func (b *fakeBus) openNVMe(name string, _ time.Duration) (smartdecode.NVMeTransport, io.Closer, error) {
	b.opened = append(b.opened, name)
	if err := b.openErr[name]; err != nil {
		return nil, nil, err
	}
	return b.nvme[name], io.NopCloser(nil), nil
}

func newBus() *fakeBus {
	return &fakeBus{
		ata:     map[string]smartdecode.ATATransport{},
		nvme:    map[string]smartdecode.NVMeTransport{},
		openErr: map[string]error{},
	}
}

func (b *fakeBus) addATA(name string, tr smartdecode.ATATransport) {
	b.devs = append(b.devs, device{name, kindSATA})
	b.ata[name] = tr
}

func (b *fakeBus) addNVMe(name string, tr smartdecode.NVMeTransport) {
	b.devs = append(b.devs, device{name, kindNVMe})
	b.nvme[name] = tr
}

type harness struct {
	scraper *scraper
	logs    *observer.ObservedLogs
	reader  *sdkmetric.ManualReader
}

func newHarness(t *testing.T, bus *fakeBus, mutate ...func(*Config)) *harness {
	t.Helper()
	cfg := createDefaultConfig().(*Config)
	for _, m := range mutate {
		m(cfg)
	}
	require.NoError(t, cfg.Validate())
	core, logs := observer.New(zapcore.DebugLevel)
	reader := sdkmetric.NewManualReader()
	counter, err := newSkippedCounter(component.TelemetrySettings{MeterProvider: sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))})
	require.NoError(t, err)
	s, err := newScraper(zap.New(core), cfg, bus, counter, time.Unix(1000, 0))
	require.NoError(t, err)
	return &harness{scraper: s, logs: logs, reader: reader}
}

func (h *harness) scrape() pmetric.Metrics {
	return h.scraper.scrape(context.Background(), time.Unix(2000, 0))
}

// skipped returns the counts of otelcol_smart_devices_skipped by reason.
func (h *harness) skipped(t *testing.T) map[string]int64 {
	t.Helper()
	var rm metricdata.ResourceMetrics
	require.NoError(t, h.reader.Collect(context.Background(), &rm))
	out := map[string]int64{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != skippedMetric {
				continue
			}
			for _, dp := range m.Data.(metricdata.Sum[int64]).DataPoints {
				reason, _ := dp.Attributes.Value("reason")
				out[reason.AsString()] += dp.Value
			}
		}
	}
	return out
}

type point struct {
	attrs map[string]string
	value float64
}

func (p point) String() string { return fmt.Sprintf("%v=%v", p.attrs, p.value) }

// points returns the data points of one metric.
func points(md pmetric.Metrics, name string) []point {
	var out []point
	for i := 0; i < md.ResourceMetrics().Len(); i++ {
		sms := md.ResourceMetrics().At(i).ScopeMetrics()
		for j := 0; j < sms.Len(); j++ {
			ms := sms.At(j).Metrics()
			for k := 0; k < ms.Len(); k++ {
				m := ms.At(k)
				if m.Name() != name {
					continue
				}
				var dps pmetric.NumberDataPointSlice
				switch m.Type() {
				case pmetric.MetricTypeGauge:
					dps = m.Gauge().DataPoints()
				case pmetric.MetricTypeSum:
					dps = m.Sum().DataPoints()
				default:
					continue
				}
				for d := 0; d < dps.Len(); d++ {
					dp := dps.At(d)
					p := point{attrs: map[string]string{}}
					dp.Attributes().Range(func(key string, v pcommon.Value) bool {
						p.attrs[key] = v.AsString()
						return true
					})
					if dp.ValueType() == pmetric.NumberDataPointValueTypeInt {
						p.value = float64(dp.IntValue())
					} else {
						p.value = dp.DoubleValue()
					}
					out = append(out, p)
				}
			}
		}
	}
	return out
}

func metricNames(md pmetric.Metrics) []string {
	seen := map[string]bool{}
	for i := 0; i < md.ResourceMetrics().Len(); i++ {
		sms := md.ResourceMetrics().At(i).ScopeMetrics()
		for j := 0; j < sms.Len(); j++ {
			for k := 0; k < sms.At(j).Metrics().Len(); k++ {
				seen[sms.At(j).Metrics().At(k).Name()] = true
			}
		}
	}
	var out []string
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// where keeps the points whose attributes include every pair given.
func where(ps []point, kv ...string) []point {
	var out []point
	for _, p := range ps {
		ok := true
		for i := 0; i+1 < len(kv); i += 2 {
			if p.attrs[kv[i]] != kv[i+1] {
				ok = false
			}
		}
		if ok {
			out = append(out, p)
		}
	}
	return out
}

func single(t *testing.T, ps []point, what string) point {
	t.Helper()
	require.Len(t, ps, 1, what)
	return ps[0]
}

func TestNVMe_IdentityTemperaturesEnduranceErrorsAndCounters(t *testing.T) {
	bus := newBus()
	bus.addNVMe("nvme0", smarttest.HealthyNVMe("SN9"))
	h := newHarness(t, bus)

	md := h.scrape()

	temps := points(md, "hw.temperature")
	require.Len(t, temps, 3)
	assert.InDelta(t, 45, single(t, where(temps, "hw.sensor_location", "composite"), "composite").value, 0)
	assert.InDelta(t, 47, single(t, where(temps, "hw.sensor_location", "sensor1"), "sensor1").value, 0)
	assert.InDelta(t, 32, single(t, where(temps, "hw.sensor_location", "sensor2"), "sensor2").value, 0)
	assert.InDelta(t, 0.93, single(t, points(md, "hw.physical_disk.endurance_utilization"), "endurance").value, 1e-9)
	assert.Equal(t, "remaining", points(md, "hw.physical_disk.endurance_utilization")[0].attrs["hw.physical_disk.state"])
	errs := points(md, "hw.errors")
	assert.InDelta(t, 3, single(t, where(errs, "error.type", "media"), "media errors").value, 0)
	assert.InDelta(t, 360000, single(t, points(md, "hw.physical_disk.power_on_time"), "power on").value, 0)
	assert.InDelta(t, 12, single(t, points(md, "hw.physical_disk.power_cycles"), "power cycles").value, 0)
	assert.InDelta(t, 5120000, single(t, where(points(md, "hw.physical_disk.io"), "disk.io.direction", "write"), "written").value, 0)
	assert.InDelta(t, 10240000, single(t, where(points(md, "hw.physical_disk.io"), "disk.io.direction", "read"), "read").value, 0)
	assert.InDelta(t, 1000204886016, single(t, points(md, "hw.physical_disk.size"), "size").value, 0)
	id := temps[0].attrs
	assert.Equal(t, "SN9", id["hw.id"])
	assert.Equal(t, "nvme0", id["hw.name"])
	assert.Equal(t, "ACME NVMe", id["hw.model"])
	assert.Equal(t, "SN9", id["hw.serial_number"])
	assert.Equal(t, "FW9", id["hw.firmware_version"])
	assert.Equal(t, "NVMe", id["hw.physical_disk.type"])
	assert.NotContains(t, id, "hw.parent")
}

func TestNVMe_CriticalWarningGivesOnePointPerState(t *testing.T) {
	cases := []struct {
		warning byte
		want    string
	}{{0x00, "ok"}, {0x02, "degraded"}, {0x01, "predicted_failure"}, {0x08, "failed"}, {0x09, "failed"}}
	for _, c := range cases {
		t.Run(c.want, func(t *testing.T) {
			ctrl := smarttest.HealthyNVMe("SN9")
			ctrl.Log = smarttest.NVMeSmartBuffer(smarttest.NVMeSmartSpec{CriticalWarning: c.warning, CompositeKelvin: 300})
			bus := newBus()
			bus.addNVMe("nvme0", ctrl)

			status := points(newHarness(t, bus).scrape(), "hw.status")

			require.Len(t, status, 4)
			for _, p := range status {
				want := 0.0
				if p.attrs["hw.state"] == c.want {
					want = 1
				}
				assert.InDelta(t, want, p.value, 0, "state %s", p.attrs["hw.state"])
				assert.Equal(t, "physical_disk", p.attrs["hw.type"])
			}
		})
	}
}

func TestNVMe_ZeroSensorGivesNoPoint(t *testing.T) {
	ctrl := smarttest.HealthyNVMe("SN9")
	ctrl.Log = smarttest.NVMeSmartBuffer(smarttest.NVMeSmartSpec{CompositeKelvin: 300})
	bus := newBus()
	bus.addNVMe("nvme0", ctrl)

	temps := points(newHarness(t, bus).scrape(), "hw.temperature")

	require.Len(t, temps, 1)
	assert.Equal(t, "composite", temps[0].attrs["hw.sensor_location"])
}

func TestNVMe_SpareAndUnsafeShutdownsAreSmartPoints(t *testing.T) {
	ctrl := smarttest.HealthyNVMe("SN9")
	ctrl.Log = smarttest.NVMeSmartBuffer(smarttest.NVMeSmartSpec{CompositeKelvin: 300, AvailableSpare: 98, SpareThreshold: 10, UnsafeShutdowns: 4})
	bus := newBus()
	bus.addNVMe("nvme0", ctrl)

	smart := points(newHarness(t, bus).scrape(), "hw.physical_disk.smart")

	assert.InDelta(t, 98, single(t, where(smart, "hw.physical_disk.smart_attribute", "Available_Spare"), "spare").value, 0)
	assert.InDelta(t, 10, single(t, where(smart, "hw.physical_disk.smart_attribute", "Available_Spare_Threshold"), "threshold").value, 0)
	assert.InDelta(t, 4, single(t, where(smart, "hw.physical_disk.smart_attribute", "Unsafe_Shutdowns"), "unsafe").value, 0)
	for _, p := range smart {
		assert.Equal(t, "raw", p.attrs["hw.physical_disk.smart_value_type"])
	}
}

func sataWith(attrs []smarttest.RawAttr, mutate ...func(*smarttest.ATA)) *smarttest.ATA {
	d := smarttest.HealthyATA("SN1")
	d.Data, d.Thresholds = smarttest.SmartDataBuffer(attrs), smarttest.ThresholdBuffer(attrs)
	for _, m := range mutate {
		m(d)
	}
	return d
}

func scrapeSATA(t *testing.T, d *smarttest.ATA, mutate ...func(*Config)) pmetric.Metrics {
	t.Helper()
	bus := newBus()
	bus.addATA("sda", d)
	return newHarness(t, bus, mutate...).scrape()
}

func TestSATA_DiskTypeFollowsTheRotationRate(t *testing.T) {
	for rate, want := range map[uint16]string{1: "SSD", 7200: "HDD"} {
		d := smarttest.HealthyATA("SN1")
		d.Identify = smarttest.IdentifyBuffer(smarttest.IdentifySpec{Model: "X", Serial: "SN1", Sectors: 1000, Rotation: rate, SMARTSupported: true, SMARTEnabled: true})

		size := points(scrapeSATA(t, d), "hw.physical_disk.size")

		assert.Equal(t, want, single(t, size, "size").attrs["hw.physical_disk.type"], "rate %d", rate)
	}
}

func TestSATA_IdentitySizeAndHealth(t *testing.T) {
	md := scrapeSATA(t, smarttest.HealthyATA("SN1"))

	size := single(t, points(md, "hw.physical_disk.size"), "size")
	assert.InDelta(t, 1000*512, size.value, 0)
	assert.Equal(t, "SN1", size.attrs["hw.id"])
	assert.Equal(t, "sda", size.attrs["hw.name"])
	assert.Equal(t, "ACME SSD", size.attrs["hw.model"])
	assert.Equal(t, "F1", size.attrs["hw.firmware_version"])
	status := where(points(md, "hw.status"), "hw.state", "ok")
	assert.InDelta(t, 1, single(t, status, "ok state").value, 0)
}

func TestSATA_ReturnStatusPredictedFailure(t *testing.T) {
	d := smarttest.HealthyATA("SN1")
	d.Status = smarttest.ATAStatus{LBAMid: 0xF4, LBAHigh: 0x2C}

	status := points(scrapeSATA(t, d), "hw.status")

	assert.InDelta(t, 1, single(t, where(status, "hw.state", "predicted_failure"), "predicted").value, 0)
	assert.InDelta(t, 0, single(t, where(status, "hw.state", "ok"), "ok").value, 0)
}

func TestSATA_SmartPointsCarryNameAndValueType(t *testing.T) {
	attrs := []smarttest.RawAttr{{ID: 5, Value: 100, Worst: 99, Threshold: 10, Raw: smarttest.RawNumber(3)}}

	smart := points(scrapeSATA(t, sataWith(attrs)), "hw.physical_disk.smart")

	require.Len(t, smart, 2, "normalized and raw by default")
	assert.InDelta(t, 100, single(t, where(smart, "hw.physical_disk.smart_value_type", "normalized"), "normalized").value, 0)
	raw := single(t, where(smart, "hw.physical_disk.smart_value_type", "raw"), "raw")
	assert.InDelta(t, 3, raw.value, 0)
	assert.Equal(t, "Reallocated_Sector_Ct", raw.attrs["hw.physical_disk.smart_attribute"])
}

func TestSATA_WorstAndThresholdOnlyWhenListed(t *testing.T) {
	attrs := []smarttest.RawAttr{{ID: 5, Value: 100, Worst: 99, Threshold: 10, Raw: smarttest.RawNumber(3)}}

	smart := points(scrapeSATA(t, sataWith(attrs), func(c *Config) {
		c.ValueTypes = []string{"normalized", "worst", "threshold", "raw"}
	}), "hw.physical_disk.smart")

	require.Len(t, smart, 4)
	assert.InDelta(t, 99, single(t, where(smart, "hw.physical_disk.smart_value_type", "worst"), "worst").value, 0)
	assert.InDelta(t, 10, single(t, where(smart, "hw.physical_disk.smart_value_type", "threshold"), "threshold").value, 0)
}

func TestSATA_DefaultListReadsExactlyTheTwentyIDs(t *testing.T) {
	table := []smarttest.RawAttr{}
	for _, id := range smartdecode.DefaultAttributeIDs() {
		table = append(table, smarttest.RawAttr{ID: id, Value: 50})
	}
	table = append(table, smarttest.RawAttr{ID: 2, Value: 50}, smarttest.RawAttr{ID: 3, Value: 50}, smarttest.RawAttr{ID: 250, Value: 50})

	def := points(scrapeSATA(t, sataWith(table), func(c *Config) { c.ValueTypes = []string{"normalized"} }), "hw.physical_disk.smart")
	all := points(scrapeSATA(t, sataWith(table), func(c *Config) {
		c.Attributes = "all"
		c.ValueTypes = []string{"normalized"}
	}), "hw.physical_disk.smart")
	list := points(scrapeSATA(t, sataWith(table), func(c *Config) {
		c.Attributes = []any{"2", 250}
		c.ValueTypes = []string{"normalized"}
	}), "hw.physical_disk.smart")

	assert.Len(t, def, 20)
	assert.Len(t, all, 23)
	assert.Len(t, list, 2)
}

func TestSATA_RawIsA48BitNumberAndUnknownIDsGetAName(t *testing.T) {
	attrs := []smarttest.RawAttr{{ID: 250, Value: 1, Raw: [6]byte{1, 2, 3, 4, 5, 6}}}

	smart := points(scrapeSATA(t, sataWith(attrs), func(c *Config) { c.Attributes = "all" }), "hw.physical_disk.smart")

	raw := single(t, where(smart, "hw.physical_disk.smart_value_type", "raw"), "raw")
	assert.InDelta(t, 6618611909121, raw.value, 0)
	assert.Equal(t, "Attribute_250", raw.attrs["hw.physical_disk.smart_attribute"])
}

func TestSATA_ErrorsFromAttributes5And197And198And199(t *testing.T) {
	attrs := []smarttest.RawAttr{
		{ID: 5, Raw: smarttest.RawNumber(1)}, {ID: 197, Raw: smarttest.RawNumber(2)},
		{ID: 198, Raw: smarttest.RawNumber(3)}, {ID: 199, Raw: smarttest.RawNumber(4)},
	}

	errs := points(scrapeSATA(t, sataWith(attrs)), "hw.errors")

	assert.Len(t, where(errs, "error.type", "bad_sector"), 3)
	crc := single(t, where(errs, "error.type", "crc"), "crc")
	assert.InDelta(t, 4, crc.value, 0)
	seen := map[string]bool{}
	for _, p := range errs {
		key := p.attrs["error.type"] + "/" + p.attrs["hw.physical_disk.smart_attribute"]
		assert.False(t, seen[key], "duplicate series %s", key)
		seen[key] = true
	}
}

func TestSATA_TemperatureFromTheLowByteOf194ThenOf190ThenTheDeviceStatistics(t *testing.T) {
	cases := map[string]struct {
		attrs []smarttest.RawAttr
		stats bool
		want  float64
	}{
		"194":               {[]smarttest.RawAttr{{ID: 194, Raw: [6]byte{38, 0, 45, 0, 20, 0}}, {ID: 190, Raw: [6]byte{50}}}, false, 38},
		"190 only":          {[]smarttest.RawAttr{{ID: 190, Raw: [6]byte{41}}}, false, 41},
		"device statistics": {[]smarttest.RawAttr{{ID: 9, Raw: smarttest.RawNumber(1)}}, true, 33},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			d := sataWith(c.attrs, func(d *smarttest.ATA) {
				page := make([]byte, 512)
				page[8], page[15] = 33, 0xC0
				d.StatsPage = page
			})

			temps := points(scrapeSATA(t, d), "hw.temperature")

			assert.InDelta(t, c.want, single(t, temps, "temperature").value, 0)
		})
	}
}

func TestSATA_PowerOnTimeAndCyclesFromAttributes9And12(t *testing.T) {
	attrs := []smarttest.RawAttr{{ID: 9, Raw: smarttest.RawNumber(100)}, {ID: 12, Raw: smarttest.RawNumber(7)}}

	md := scrapeSATA(t, sataWith(attrs))

	assert.InDelta(t, 360000, single(t, points(md, "hw.physical_disk.power_on_time"), "power on").value, 0)
	assert.InDelta(t, 7, single(t, points(md, "hw.physical_disk.power_cycles"), "cycles").value, 0)
}

func TestSATA_EnduranceFrom231Else233Else202(t *testing.T) {
	cases := map[string]struct {
		attrs []smarttest.RawAttr
		want  float64
		none  bool
	}{
		"231":      {[]smarttest.RawAttr{{ID: 231, Value: 93}}, 0.93, false},
		"233":      {[]smarttest.RawAttr{{ID: 233, Value: 88}}, 0.88, false},
		"202 only": {[]smarttest.RawAttr{{ID: 202, Value: 77}}, 0.77, false},
		"none":     {[]smarttest.RawAttr{{ID: 9, Value: 1}}, 0, true},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			ps := points(scrapeSATA(t, sataWith(c.attrs)), "hw.physical_disk.endurance_utilization")

			if c.none {
				assert.Empty(t, ps)
				return
			}
			p := single(t, ps, "endurance")
			assert.InDelta(t, c.want, p.value, 1e-9)
			assert.Equal(t, "remaining", p.attrs["hw.physical_disk.state"])
		})
	}
}

func TestSATA_NoEnduranceOnARotatingDisk(t *testing.T) {
	d := sataWith([]smarttest.RawAttr{{ID: 231, Value: 93}})
	d.Identify = smarttest.IdentifyBuffer(smarttest.IdentifySpec{Model: "X", Serial: "SN1", Sectors: 1000, Rotation: 7200, SMARTSupported: true, SMARTEnabled: true})

	assert.Empty(t, points(scrapeSATA(t, d), "hw.physical_disk.endurance_utilization"))
}

func TestSATA_DataMovedFromAttributes241And242OnlyWhenTheUnitIsNamed(t *testing.T) {
	attrs := []smarttest.RawAttr{{ID: 241, Raw: smarttest.RawNumber(1000)}, {ID: 242, Raw: smarttest.RawNumber(2000)}}
	d := sataWith(attrs)

	io := points(scrapeSATA(t, d), "hw.physical_disk.io")

	assert.InDelta(t, 1000*512, single(t, where(io, "disk.io.direction", "write"), "write").value, 0)
	assert.InDelta(t, 2000*512, single(t, where(io, "disk.io.direction", "read"), "read").value, 0)

	bus := newBus()
	bus.addATA("sda", sataWith(attrs))
	h := newHarness(t, bus)
	h.scraper.ioUnit = func(uint8) (uint64, bool) { return 0, false }
	assert.Empty(t, points(h.scrape(), "hw.physical_disk.io"))
}

func TestSATA_StandbyDiskGetsOnlyCheckPowerModeAndIsCounted(t *testing.T) {
	d := smarttest.HealthyATA("SN1")
	d.PowerMode = 0x00
	bus := newBus()
	bus.addATA("sda", d)
	h := newHarness(t, bus)

	md := h.scrape()

	assert.Equal(t, 0, md.MetricCount())
	assert.Equal(t, []uint8{0xE5}, d.Commands())
	assert.Equal(t, map[string]int64{"standby": 1}, h.skipped(t))
}

func TestSATA_StandbyIsReadWhenSkipIfStandbyIsOff(t *testing.T) {
	d := smarttest.HealthyATA("SN1")
	d.PowerMode = 0x00

	md := scrapeSATA(t, d, func(c *Config) { c.SkipIfStandby = false })

	assert.NotZero(t, md.MetricCount())
}

func TestSATA_UnsupportedAndDisabledSmartAreCountedAndNeverEnabled(t *testing.T) {
	noSmart := smarttest.HealthyATA("SN1")
	noSmart.Identify = smarttest.IdentifyBuffer(smarttest.IdentifySpec{Model: "X", Serial: "SN1", Sectors: 1})
	disabled := smarttest.HealthyATA("SN2")
	disabled.Identify = smarttest.IdentifyBuffer(smarttest.IdentifySpec{Model: "X", Serial: "SN2", Sectors: 1, SMARTSupported: true})
	bus := newBus()
	bus.addATA("sda", noSmart)
	bus.addATA("sdb", disabled)
	h := newHarness(t, bus)

	md := h.scrape()

	assert.Equal(t, 0, md.MetricCount())
	assert.Equal(t, map[string]int64{"unsupported": 1, "smart_disabled": 1}, h.skipped(t))
	for _, d := range []*smarttest.ATA{noSmart, disabled} {
		for _, c := range d.Sent {
			assert.NotEqual(t, uint8(0xB0), c.Command, "a SMART command reached a disk without SMART")
		}
	}
}

func TestSATA_VirtualRaidAndBridgeDevicesAreSkippedAsUnsupported(t *testing.T) {
	bus := newBus()
	for _, name := range []string{"sda", "sdb", "vda"} {
		bus.addATA(name, &smarttest.ATA{FailWith: smartdecode.ErrUnsupported})
	}
	h := newHarness(t, bus)

	md := h.scrape()

	assert.Equal(t, 0, md.MetricCount())
	assert.Equal(t, map[string]int64{"unsupported": 3}, h.skipped(t))
}

func TestTimeoutSkipsOneDeviceAndTheNextIsRead(t *testing.T) {
	bus := newBus()
	bus.addATA("sda", &smarttest.ATA{FailWith: smartdecode.ErrTimeout})
	bus.addNVMe("nvme0", smarttest.HealthyNVMe("SN9"))
	h := newHarness(t, bus)

	md := h.scrape()

	assert.NotZero(t, len(points(md, "hw.temperature")))
	assert.Equal(t, map[string]int64{"timeout": 1}, h.skipped(t))
}

func TestPermissionErrorNamesTheDeviceAndTheCapabilityOncePerEpisode(t *testing.T) {
	bus := newBus()
	bus.addNVMe("nvme0", &smarttest.NVMe{FailWith: smartdecode.ErrPermission})
	bus.addATA("sda", &smarttest.ATA{FailWith: smartdecode.ErrPermission})
	bus.addATA("sdb", smarttest.HealthyATA("SN2"))
	h := newHarness(t, bus)

	first := h.scrape()
	h.scrape()

	errs := h.logs.FilterLevelExact(zapcore.ErrorLevel).All()
	require.Len(t, errs, 2, "one error for each device, none for the second interval")
	text := fmt.Sprint(errs[0].Message, errs[0].ContextMap(), errs[1].Message, errs[1].ContextMap())
	assert.Contains(t, text, "nvme0")
	assert.Contains(t, text, "CAP_SYS_ADMIN")
	assert.Contains(t, text, "sda")
	assert.Contains(t, text, "CAP_SYS_RAWIO")
	assert.NotZero(t, len(points(first, "hw.physical_disk.size")), "the third device is still read")
}

func TestPermissionEpisodeEndsWhenTheDeviceReadsAgain(t *testing.T) {
	bus := newBus()
	failing := &smarttest.NVMe{FailWith: smartdecode.ErrPermission}
	bus.addNVMe("nvme0", failing)
	h := newHarness(t, bus)
	h.scrape()
	healthy := smarttest.HealthyNVMe("SN9")
	bus.nvme["nvme0"] = healthy
	h.scrape()
	bus.nvme["nvme0"] = failing

	h.scrape()

	assert.Len(t, h.logs.FilterLevelExact(zapcore.ErrorLevel).All(), 2, "a new episode logs again")
}

func TestOpenFailureIsReportedLikeAReadFailure(t *testing.T) {
	bus := newBus()
	bus.addATA("sda", smarttest.HealthyATA("SN1"))
	bus.openErr["sda"] = fmt.Errorf("open /dev/sda: %w", smartdecode.ErrPermission)
	h := newHarness(t, bus)

	h.scrape()

	assert.Len(t, h.logs.FilterLevelExact(zapcore.ErrorLevel).All(), 1)
	assert.Equal(t, map[string]int64{"permission": 1}, h.skipped(t))
}

func TestAFileAccessRefusalAsksForReadAccessAndNotForACapability(t *testing.T) {
	bus := newBus()
	bus.addNVMe("nvme0", smarttest.HealthyNVMe("SN9"))
	bus.openErr["nvme0"] = fmt.Errorf("open /dev/nvme0: %w: %w", smartdecode.ErrFileAccess, smartdecode.ErrPermission)
	h := newHarness(t, bus)

	h.scrape()

	errs := h.logs.FilterLevelExact(zapcore.ErrorLevel).All()
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0].Message, "run the collector as root, or give its user read access")
	assert.NotContains(t, errs[0].ContextMap(), "missing_capability")
	assert.Equal(t, map[string]int64{"permission": 1}, h.skipped(t), "the metric keeps one reason for both kinds of refusal")
}

func TestAKernelRefusalNamesTheCapabilityAndNotTheFile(t *testing.T) {
	bus := newBus()
	bus.addNVMe("nvme0", &smarttest.NVMe{FailWith: smartdecode.ErrPermission})
	h := newHarness(t, bus)

	h.scrape()

	errs := h.logs.FilterLevelExact(zapcore.ErrorLevel).All()
	require.Len(t, errs, 1)
	assert.Equal(t, "CAP_SYS_ADMIN", errs[0].ContextMap()["missing_capability"])
	assert.NotContains(t, errs[0].Message, "read access")
}

func TestAChangeOfFailureKindStartsANewEpisodeAndLogsAgain(t *testing.T) {
	bus := newBus()
	dev := &smarttest.NVMe{FailWith: smartdecode.ErrTimeout}
	bus.addNVMe("nvme0", dev)
	h := newHarness(t, bus)

	h.scrape()
	h.scrape()
	assert.Len(t, h.logs.FilterLevelExact(zapcore.WarnLevel).All(), 1, "one warning for the timeout episode")
	dev.FailWith = smartdecode.ErrPermission
	h.scrape()

	assert.Len(t, h.logs.FilterLevelExact(zapcore.ErrorLevel).All(), 1, "the new kind of failure is logged")
}

func TestUnsupportedAndDisabledSmartAreLoggedOnceAtInfoLevel(t *testing.T) {
	for name, cause := range map[string]error{"unsupported": smartdecode.ErrSMARTUnsupported, "smart_disabled": smartdecode.ErrSMARTDisabled} {
		t.Run(name, func(t *testing.T) {
			bus := newBus()
			bus.addATA("sda", &smarttest.ATA{FailWith: cause})
			h := newHarness(t, bus)

			h.scrape()
			h.scrape()

			infos := h.logs.FilterLevelExact(zapcore.InfoLevel).All()
			require.Len(t, infos, 1)
			assert.Equal(t, name, infos[0].ContextMap()["reason"])
		})
	}
}

func TestACancelledContextReadsNoFurtherDevice(t *testing.T) {
	bus := newBus()
	bus.addATA("sda", smarttest.HealthyATA("SN1"))
	bus.addATA("sdb", smarttest.HealthyATA("SN2"))
	h := newHarness(t, bus)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	h.scraper.scrape(ctx, time.Unix(2000, 0))

	assert.Empty(t, bus.opened)
}

func TestNVMe_AZeroCompositeGivesNoTemperatureAndAWornOutDriveStopsAtZeroRemaining(t *testing.T) {
	ctrl := smarttest.HealthyNVMe("SN9")
	ctrl.Log = smarttest.NVMeSmartBuffer(smarttest.NVMeSmartSpec{CompositeKelvin: 0, PercentageUsed: 130, SensorsKelvin: [8]uint16{310}})
	bus := newBus()
	bus.addNVMe("nvme0", ctrl)

	md := newHarness(t, bus).scrape()

	temps := points(md, "hw.temperature")
	require.Len(t, temps, 1, "the sensor only")
	assert.Equal(t, "sensor1", temps[0].attrs["hw.sensor_location"])
	assert.InDelta(t, 0, single(t, points(md, "hw.physical_disk.endurance_utilization"), "endurance").value, 1e-9)
}

func TestMaxDevicesCapsTheReadAndWarnsOncePerEpisode(t *testing.T) {
	bus := newBus()
	for i := 0; i < 70; i++ {
		bus.addATA(fmt.Sprintf("sd%c%c", 'a'+i/26, 'a'+i%26), smarttest.HealthyATA(fmt.Sprintf("SN%d", i)))
	}
	h := newHarness(t, bus)

	h.scrape()
	h.scrape()

	assert.Len(t, bus.opened, 128, "64 devices a pass")
	warns := h.logs.FilterLevelExact(zapcore.WarnLevel).FilterMessageSnippet("max_devices").All()
	assert.Len(t, warns, 1)
}

func TestMaxDevicesWarningReturnsAfterTheCountDropsBelowTheCap(t *testing.T) {
	bus := newBus()
	for i := 0; i < 70; i++ {
		bus.addATA(fmt.Sprintf("sd%c%c", 'a'+i/26, 'a'+i%26), smarttest.HealthyATA(fmt.Sprintf("SN%d", i)))
	}
	h := newHarness(t, bus)
	h.scrape()
	all := bus.devs
	bus.devs = all[:10]
	h.scrape()
	bus.devs = all

	h.scrape()

	assert.Len(t, h.logs.FilterLevelExact(zapcore.WarnLevel).FilterMessageSnippet("max_devices").All(), 2)
}

func TestIncludeAndExcludeSelectTheDevicesRead(t *testing.T) {
	bus := newBus()
	bus.addNVMe("nvme0", smarttest.HealthyNVMe("SN9"))
	bus.addATA("sda", smarttest.HealthyATA("SN1"))
	bus.addATA("sdb", smarttest.HealthyATA("SN2"))

	nvmeOnly := newHarness(t, bus, func(c *Config) { c.Include = []string{"^nvme[0-9]+$"} })
	nvmeOnly.scrape()
	assert.Equal(t, []string{"nvme0"}, bus.opened)

	bus.opened = nil
	h := newHarness(t, bus, func(c *Config) { c.Exclude = []string{"^sdb$"} })
	h.scrape()
	assert.Equal(t, []string{"nvme0", "sda"}, bus.opened)

	bus.opened = nil
	newHarness(t, bus).scrape()
	assert.Equal(t, []string{"nvme0", "sda", "sdb"}, bus.opened)
}

func TestHwIDFallsBackToTheWWNThenTheKernelName(t *testing.T) {
	withWWN := smarttest.HealthyATA("")
	withWWN.Identify = smarttest.IdentifyBuffer(smarttest.IdentifySpec{Model: "X", Sectors: 1, WWN: 0x5000c5001234abcd, SMARTSupported: true, SMARTEnabled: true})
	neither := smarttest.HealthyATA("")
	neither.Identify = smarttest.IdentifyBuffer(smarttest.IdentifySpec{Model: "X", Sectors: 1, SMARTSupported: true, SMARTEnabled: true})
	bus := newBus()
	bus.addATA("sda", smarttest.HealthyATA("SNX"))
	bus.addATA("sdb", withWWN)
	bus.addATA("sdc", neither)

	size := points(newHarness(t, bus).scrape(), "hw.physical_disk.size")

	ids := map[string]string{}
	for _, p := range size {
		ids[p.attrs["hw.name"]] = p.attrs["hw.id"]
		assert.NotContains(t, p.attrs, "hw.parent")
	}
	assert.Equal(t, map[string]string{"sda": "SNX", "sdb": "5000c5001234abcd", "sdc": "sdc"}, ids)
}

func TestEveryMetricNameStartsWithHW(t *testing.T) {
	bus := newBus()
	bus.addNVMe("nvme0", smarttest.HealthyNVMe("SN9"))
	bus.addATA("sda", smarttest.HealthyATA("SN1"))

	for _, name := range metricNames(newHarness(t, bus).scrape()) {
		assert.True(t, strings.HasPrefix(name, "hw."), "metric %q", name)
	}
}

func TestSumsCarryTheReceiverStartAsTheirStartTime(t *testing.T) {
	bus := newBus()
	bus.addNVMe("nvme0", smarttest.HealthyNVMe("SN9"))

	md := newHarness(t, bus).scrape()

	m := md.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics()
	for i := 0; i < m.Len(); i++ {
		if m.At(i).Type() != pmetric.MetricTypeSum {
			continue
		}
		assert.True(t, m.At(i).Sum().IsMonotonic(), m.At(i).Name())
		assert.Equal(t, pmetric.AggregationTemporalityCumulative, m.At(i).Sum().AggregationTemporality())
		assert.Equal(t, int64(1000), int64(m.At(i).Sum().DataPoints().At(0).StartTimestamp())/1e9)
	}
}

func TestAReadErrorOfAnotherKindIsCountedAndTheScrapeContinues(t *testing.T) {
	bus := newBus()
	bus.addATA("sda", &smarttest.ATA{FailWith: errors.New("boom")})
	bus.addNVMe("nvme0", smarttest.HealthyNVMe("SN9"))
	h := newHarness(t, bus)

	md := h.scrape()

	assert.NotZero(t, len(points(md, "hw.temperature")))
	assert.Equal(t, map[string]int64{"error": 1}, h.skipped(t))
	assert.Len(t, h.logs.FilterLevelExact(zapcore.WarnLevel).All(), 1)
}
