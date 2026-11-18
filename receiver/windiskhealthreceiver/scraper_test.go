// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package windiskhealthreceiver

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/pdata/pmetric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/vladistan/otelcol-custom/receiver/hwschema"
	"github.com/vladistan/otelcol-custom/receiver/smartdecode"
	"github.com/vladistan/otelcol-custom/receiver/smartdecode/smarttest"
)

// fakeSession is a disk with canned answers; sent counts every call that would reach the disk.
type fakeSession struct {
	desc    descriptor
	descErr error
	temp    int
	tempErr error
	failing bool
	predErr error
	ata     smartdecode.ATATransport
	nvme    smartdecode.NVMeTransport
	sent    int
	closed  bool
}

func (f *fakeSession) Close() error { f.closed = true; return nil }

func (f *fakeSession) descriptor() (descriptor, error) {
	f.sent++
	return f.desc, f.descErr
}

func (f *fakeSession) temperature() (int, error) {
	f.sent++
	return f.temp, f.tempErr
}

func (f *fakeSession) predictFailure() (bool, error) {
	f.sent++
	return f.failing, f.predErr
}

func (f *fakeSession) ATA(cmd smartdecode.ATACommand) (smartdecode.ATAResult, error) {
	f.sent++
	if f.ata == nil {
		return smartdecode.ATAResult{}, smartdecode.ErrUnsupported
	}
	return f.ata.ATA(cmd)
}

func (f *fakeSession) Admin(cmd smartdecode.NVMeCommand) ([]byte, error) {
	f.sent++
	if f.nvme == nil {
		return nil, smartdecode.ErrUnsupported
	}
	return f.nvme.Admin(cmd)
}

type fakeBus struct {
	names     []string
	sessions  map[string]*fakeSession
	openErr   map[string]error
	opened    []string
	discovers int
}

func newFakeBus() *fakeBus {
	return &fakeBus{sessions: map[string]*fakeSession{}, openErr: map[string]error{}}
}

func (b *fakeBus) add(name string, s *fakeSession) *fakeSession {
	b.names = append(b.names, name)
	b.sessions[name] = s
	return s
}

func (b *fakeBus) discover() ([]string, error) { b.discovers++; return b.names, nil }

func (b *fakeBus) open(name string, _ time.Duration) (session, error) {
	b.opened = append(b.opened, name)
	if err := b.openErr[name]; err != nil {
		return nil, err
	}
	return b.sessions[name], nil
}

func sataDisk(ata smartdecode.ATATransport) *fakeSession {
	return &fakeSession{desc: descriptor{busType: 11, product: "ACME SSD"}, temp: 41, ata: ata}
}

func nvmeDisk(nvme smartdecode.NVMeTransport) *fakeSession {
	return &fakeSession{desc: descriptor{busType: busNVMe, product: "ACME NVMe"}, temp: 44, nvme: nvme}
}

type harness struct {
	scraper *scraper
	logs    *observer.ObservedLogs
	reader  *sdkmetric.ManualReader
}

func newHarness(t *testing.T, b *fakeBus, mutate ...func(*Config)) *harness {
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
	s, err := newScraper(zap.New(core), cfg, b, counter, time.Unix(1000, 0))
	require.NoError(t, err)
	return &harness{scraper: s, logs: logs, reader: reader}
}

func (h *harness) scrape() pmetric.Metrics {
	return h.scraper.scrape(context.Background(), time.Unix(2000, 0))
}

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
				reason, _ := dp.Attributes.Value(reasonLabelKey)
				out[reason.AsString()] += dp.Value
			}
		}
	}
	return out
}

func (h *harness) logged(level zapcore.Level) []observer.LoggedEntry {
	return h.logs.FilterLevelExact(level).All()
}

// points returns the data points of a metric as "attr=value,..." strings mapped to their values.
func points(md pmetric.Metrics, name string) map[string]float64 {
	out := map[string]float64{}
	rms := md.ResourceMetrics()
	for i := 0; i < rms.Len(); i++ {
		sms := rms.At(i).ScopeMetrics()
		for j := 0; j < sms.Len(); j++ {
			ms := sms.At(j).Metrics()
			for k := 0; k < ms.Len(); k++ {
				m := ms.At(k)
				if m.Name() != name {
					continue
				}
				var dps pmetric.NumberDataPointSlice
				if m.Type() == pmetric.MetricTypeSum {
					dps = m.Sum().DataPoints()
				} else {
					dps = m.Gauge().DataPoints()
				}
				for d := 0; d < dps.Len(); d++ {
					dp := dps.At(d)
					v := dp.DoubleValue()
					if dp.ValueType() == pmetric.NumberDataPointValueTypeInt {
						v = float64(dp.IntValue())
					}
					out[attrKey(dp.Attributes().AsRaw())] = v
				}
			}
		}
	}
	return out
}

func attrKey(raw map[string]any) string {
	keys := make([]string, 0, len(raw))
	for k := range raw {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	s := ""
	for _, k := range keys {
		s += fmt.Sprintf("%s=%v,", k, raw[k])
	}
	return s
}

func sortedNames(names ...string) []string {
	sort.Strings(names)
	return names
}

func metricNames(md pmetric.Metrics) []string {
	var out []string
	ms := md.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics()
	for i := 0; i < ms.Len(); i++ {
		out = append(out, ms.At(i).Name())
	}
	sort.Strings(out)
	return out
}

// only returns the one value of a metric that has exactly one data point.
func only(t *testing.T, md pmetric.Metrics, name string) float64 {
	t.Helper()
	p := points(md, name)
	require.Len(t, p, 1, name)
	for _, v := range p {
		return v
	}
	return 0
}

func statusOf(md pmetric.Metrics, state string) float64 {
	for k, v := range points(md, hwschema.MetricStatus) {
		if strings.Contains(k, hwschema.AttrState+"="+state+",") {
			return v
		}
	}
	return -1
}

func TestScrape_TemperatureFromTheStorageProperty(t *testing.T) {
	b := newFakeBus()
	b.add("PhysicalDrive0", &fakeSession{desc: descriptor{busType: 11, product: "ACME SSD"}, temp: 37, ata: &smarttest.ATA{FailWith: smartdecode.ErrUnsupported}})
	md := newHarness(t, b).scrape()
	assert.Equal(t, 37.0, only(t, md, hwschema.MetricTemperature))
}

func TestScrape_FailurePredictionGivesTheStatus(t *testing.T) {
	for _, c := range []struct {
		failing bool
		want    string
	}{{false, hwschema.StateOK}, {true, hwschema.StatePredictedFailure}} {
		b := newFakeBus()
		b.add("PhysicalDrive0", &fakeSession{desc: descriptor{busType: 11}, temp: 30, failing: c.failing, ata: &smarttest.ATA{FailWith: smartdecode.ErrUnsupported}})
		md := newHarness(t, b).scrape()
		assert.Equal(t, 1.0, statusOf(md, c.want), c.want)
		assert.Equal(t, 0.0, statusOf(md, hwschema.StateFailed))
	}
}

func TestScrape_AnATASmartBufferGivesTheSeriesTheSmartReceiverGives(t *testing.T) {
	b := newFakeBus()
	b.add("PhysicalDrive0", sataDisk(smarttest.HealthyATA("SN1")))
	md := newHarness(t, b).scrape()

	assert.Equal(t, sortedNames(
		hwschema.MetricPhysicalDiskEndurance, hwschema.MetricPhysicalDiskPowerCycles, hwschema.MetricPhysicalDiskPowerOnTime,
		hwschema.MetricPhysicalDiskSize, hwschema.MetricPhysicalDiskSmart, hwschema.MetricErrors, hwschema.MetricStatus, hwschema.MetricTemperature,
	), metricNames(md))
	assert.Equal(t, 1.0, statusOf(md, hwschema.StateOK))
	assert.Equal(t, 0.93, only(t, md, hwschema.MetricPhysicalDiskEndurance))
	assert.Equal(t, 12.0, only(t, md, hwschema.MetricPhysicalDiskPowerCycles))
	assert.Equal(t, 100.0*3600, only(t, md, hwschema.MetricPhysicalDiskPowerOnTime))

	for key := range points(md, hwschema.MetricPhysicalDiskSize) {
		assert.Contains(t, key, hwschema.AttrID+"=SN1,")
		assert.Contains(t, key, hwschema.AttrSerialNumber+"=SN1,")
		assert.Contains(t, key, hwschema.AttrModel+"=ACME SSD,")
		assert.Contains(t, key, hwschema.AttrPhysicalDiskType+"="+hwschema.DiskSSD+",")
	}
	smartKeys := points(md, hwschema.MetricPhysicalDiskSmart)
	assert.Contains(t, smartKeys, attrKey(map[string]any{
		hwschema.AttrID: "SN1", hwschema.AttrName: "PhysicalDrive0", hwschema.AttrModel: "ACME SSD", hwschema.AttrSerialNumber: "SN1",
		hwschema.AttrFirmwareVersion: "F1", hwschema.AttrPhysicalDiskType: hwschema.DiskSSD,
		hwschema.AttrSmartAttribute: smartdecode.AttributeName(9), hwschema.AttrSmartValueType: hwschema.ValueRaw,
	}))
}

func TestScrape_TheStorageTemperatureWinsOverTheSmartTemperature(t *testing.T) {
	b := newFakeBus()
	b.add("PhysicalDrive0", sataDisk(smarttest.HealthyATA("SN1")))
	assert.Equal(t, 41.0, only(t, newHarness(t, b).scrape(), hwschema.MetricTemperature))

	b = newFakeBus()
	d := sataDisk(smarttest.HealthyATA("SN1"))
	d.tempErr = smartdecode.ErrUnsupported
	b.add("PhysicalDrive0", d)
	assert.Equal(t, 38.0, only(t, newHarness(t, b).scrape(), hwschema.MetricTemperature), "the SMART value is the fallback")
}

func TestScrape_AnNVMeLogPageGivesTheSeriesTheSmartReceiverGives(t *testing.T) {
	b := newFakeBus()
	b.add("PhysicalDrive1", nvmeDisk(smarttest.HealthyNVMe("NV1")))
	md := newHarness(t, b).scrape()

	assert.Equal(t, sortedNames(
		hwschema.MetricPhysicalDiskEndurance, hwschema.MetricPhysicalDiskIO, hwschema.MetricPhysicalDiskPowerCycles, hwschema.MetricPhysicalDiskPowerOnTime,
		hwschema.MetricPhysicalDiskSize, hwschema.MetricPhysicalDiskSmart, hwschema.MetricErrors, hwschema.MetricStatus, hwschema.MetricTemperature,
	), metricNames(md))
	assert.InDelta(t, 0.93, only(t, md, hwschema.MetricPhysicalDiskEndurance), 1e-9)
	assert.Equal(t, 3.0, only(t, md, hwschema.MetricErrors))

	temps := points(md, hwschema.MetricTemperature)
	require.Len(t, temps, 3, "the property gives composite, the log page the two sensors")
	for key, want := range map[string]float64{hwschema.AttrSensorLocation + "=composite,": 44, hwschema.AttrSensorLocation + "=sensor1,": 47, hwschema.AttrSensorLocation + "=sensor2,": 32} {
		found := false
		for k, v := range temps {
			if strings.Contains(k, key) {
				found = true
				assert.Equal(t, want, v, key)
			}
		}
		assert.True(t, found, key)
	}
	for key := range points(md, hwschema.MetricPhysicalDiskSize) {
		assert.Contains(t, key, hwschema.AttrPhysicalDiskType+"="+hwschema.DiskNVMe+",")
	}
}

func TestScrape_AnUnsupportedFailurePredictionStillGivesTemperatureAndSmart(t *testing.T) {
	b := newFakeBus()
	d := sataDisk(smarttest.HealthyATA("SN1"))
	d.predErr = fmt.Errorf("query the failure prediction: %w", smartdecode.ErrUnsupported)
	b.add("PhysicalDrive0", d)
	h := newHarness(t, b)
	md := h.scrape()

	assert.Contains(t, metricNames(md), hwschema.MetricPhysicalDiskSmart)
	assert.Equal(t, 41.0, only(t, md, hwschema.MetricTemperature))
	assert.Equal(t, 1.0, statusOf(md, hwschema.StateOK), "the SMART health still gives the status")
	assert.Equal(t, map[string]int64{reasonUnsupported: 1}, h.skipped(t))
}

func TestScrape_AnAccessDeniedGivesOneErrorPerDiskAndTheOtherDiskIsRead(t *testing.T) {
	b := newFakeBus()
	b.add("PhysicalDrive0", &fakeSession{desc: descriptor{busType: 11}, tempErr: fmt.Errorf("open: %w", smartdecode.ErrPermission)})
	b.add("PhysicalDrive1", sataDisk(smarttest.HealthyATA("SN2")))
	h := newHarness(t, b)

	h.scrape()
	md := h.scrape()

	errs := h.logged(zapcore.ErrorLevel)
	require.Len(t, errs, 1, "one error across two scrapes")
	assert.Equal(t, "PhysicalDrive0", errs[0].ContextMap()["device"])
	assert.Contains(t, errs[0].ContextMap()["missing_privilege"], "administrator")
	assert.Equal(t, map[string]int64{reasonPermission: 2}, h.skipped(t))
	for key := range points(md, hwschema.MetricPhysicalDiskSize) {
		assert.Contains(t, key, "SN2")
	}
}

func TestScrape_ATimeoutSkipsTheDiskAndSendsNothingMore(t *testing.T) {
	b := newFakeBus()
	d := b.add("PhysicalDrive0", &fakeSession{desc: descriptor{busType: 11}, tempErr: smartdecode.ErrTimeout, ata: smarttest.HealthyATA("SN1")})
	h := newHarness(t, b)
	md := h.scrape()

	assert.Equal(t, 0, md.MetricCount())
	assert.Equal(t, map[string]int64{reasonTimeout: 1}, h.skipped(t))
	assert.Equal(t, 2, d.sent, "the descriptor and the temperature only")
	assert.True(t, d.closed)
}

func TestScrape_ARaidVirtualDriveIsSkippedWithoutACommand(t *testing.T) {
	b := newFakeBus()
	d := b.add("PhysicalDrive0", &fakeSession{desc: descriptor{busType: busRAID}})
	h := newHarness(t, b)
	assert.Equal(t, 0, h.scrape().MetricCount())
	assert.Equal(t, map[string]int64{reasonUnsupported: 1}, h.skipped(t))
	assert.Equal(t, 1, d.sent, "only the descriptor query")
}

func TestScrape_AnNVMeDiskBehindTheDriverThatRefusesIsSkippedOnceAndTheOtherDiskIsRead(t *testing.T) {
	b := newFakeBus()
	rst := b.add("PhysicalDrive1", &fakeSession{
		desc: descriptor{busType: busNVMe}, tempErr: smartdecode.ErrUnsupported, predErr: smartdecode.ErrUnsupported,
		nvme: &smarttest.NVMe{FailWith: smartdecode.ErrUnsupported},
	})
	b.add("PhysicalDrive0", sataDisk(smarttest.HealthyATA("SN1")))
	h := newHarness(t, b)

	h.scrape()
	md := h.scrape()

	assert.Equal(t, map[string]int64{reasonUnsupported: 6}, h.skipped(t))
	infos := h.logged(zapcore.InfoLevel)
	require.Len(t, infos, 1, "one info line per episode")
	assert.Equal(t, "PhysicalDrive1", infos[0].ContextMap()["device"])
	assert.Empty(t, h.logged(zapcore.ErrorLevel))
	assert.True(t, rst.closed)
	assert.NotEmpty(t, points(md, hwschema.MetricPhysicalDiskSize))
}

func TestScrape_ADiskThatRecoversEndsItsEpisode(t *testing.T) {
	b := newFakeBus()
	d := b.add("PhysicalDrive0", sataDisk(smarttest.HealthyATA("SN1")))
	d.predErr = smartdecode.ErrUnsupported
	h := newHarness(t, b)
	h.scrape()
	d.predErr = nil
	h.scrape()
	d.predErr = smartdecode.ErrUnsupported
	h.scrape()
	assert.Len(t, h.logged(zapcore.InfoLevel), 2, "the failure after the recovery is a new episode")
}

func TestScrape_AnOpenErrorIsSkippedByReason(t *testing.T) {
	b := newFakeBus()
	b.names = []string{"PhysicalDrive0"}
	b.openErr["PhysicalDrive0"] = fmt.Errorf("open: %w", smartdecode.ErrPermission)
	h := newHarness(t, b)
	assert.Equal(t, 0, h.scrape().MetricCount())
	assert.Equal(t, map[string]int64{reasonPermission: 1}, h.skipped(t))
}

func TestScrape_AnUnclassifiedErrorCountsAsError(t *testing.T) {
	b := newFakeBus()
	b.names = []string{"PhysicalDrive0"}
	b.openErr["PhysicalDrive0"] = errors.New("boom")
	h := newHarness(t, b)
	h.scrape()
	assert.Equal(t, map[string]int64{reasonError: 1}, h.skipped(t))
	assert.Len(t, h.logged(zapcore.WarnLevel), 1)
}

func TestScrape_IncludeExcludeAndMaxDevices(t *testing.T) {
	b := newFakeBus()
	for i, sn := range []string{"A", "B", "C"} {
		b.add(fmt.Sprintf("PhysicalDrive%d", i), sataDisk(smarttest.HealthyATA(sn)))
	}
	newHarness(t, b, func(c *Config) { c.Include = []string{"Drive[01]$"}; c.Exclude = []string{"Drive1$"} }).scrape()
	assert.Equal(t, []string{"PhysicalDrive0"}, b.opened)

	b.opened = nil
	h := newHarness(t, b, func(c *Config) { c.MaxDevices = 2 })
	h.scrape()
	h.scrape()
	assert.Equal(t, []string{"PhysicalDrive0", "PhysicalDrive1", "PhysicalDrive0", "PhysicalDrive1"}, b.opened)
	assert.Len(t, h.logged(zapcore.WarnLevel), 1, "the cap is logged once")
}

func TestScrape_NoDiskNoMetrics(t *testing.T) {
	assert.Equal(t, 0, newHarness(t, newFakeBus()).scrape().MetricCount())
}
