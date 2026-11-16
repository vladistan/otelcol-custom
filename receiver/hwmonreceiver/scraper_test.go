// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package hwmonreceiver

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// fakeSource returns fixed readings or an error, and counts the reads.
type fakeSource struct {
	readings []reading
	err      error
	reads    atomic.Int32
	opts     readOptions
}

func (f *fakeSource) read(opts readOptions) ([]reading, error) {
	f.reads.Add(1)
	f.opts = opts
	return f.readings, f.err
}

func testConfig() *Config { return createDefaultConfig().(*Config) }

func sampleReadings() []reading {
	return []reading{
		{kind: kindTemperature, id: "nvme/temp1", name: "Composite", parent: "nvme", value: 40.5,
			limits: []limit{{"max", 84.85}, {"critical", 89.85}}},
		{kind: kindTemperature, id: "coretemp/temp1", name: "Package id 0", parent: "coretemp", value: 50},
		{kind: kindFan, id: "nct6775/fan1", name: "CPU fan", parent: "nct6775", value: 1200},
		{kind: kindVoltage, id: "nct6775/in0", name: "in0", parent: "nct6775", value: 1.2},
	}
}

func metricByName(t *testing.T, md pmetric.Metrics, name string) pmetric.Metric {
	t.Helper()
	require.Equal(t, 1, md.ResourceMetrics().Len())
	ms := md.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics()
	for i := 0; i < ms.Len(); i++ {
		if ms.At(i).Name() == name {
			return ms.At(i)
		}
	}
	t.Fatalf("metric %s not found", name)
	return pmetric.Metric{}
}

func dpByID(t *testing.T, dps pmetric.NumberDataPointSlice, id string) pmetric.NumberDataPoint {
	t.Helper()
	for i := 0; i < dps.Len(); i++ {
		if v, ok := dps.At(i).Attributes().Get("hw.id"); ok && v.Str() == id {
			return dps.At(i)
		}
	}
	t.Fatalf("no data point with hw.id %s", id)
	return pmetric.NumberDataPoint{}
}

func TestScrapeBuildsTheFourMetrics(t *testing.T) {
	s := newScraper(zap.NewNop(), testConfig(), &fakeSource{readings: sampleReadings()})
	now := time.Unix(1700000000, 0)

	md := s.scrape(now)

	assert.Equal(t, 4, md.MetricCount())
	scope := md.ResourceMetrics().At(0).ScopeMetrics().At(0).Scope()
	assert.Equal(t, scopeName, scope.Name())

	temp := metricByName(t, md, "hw.temperature")
	assert.Equal(t, "Cel", temp.Unit())
	require.Equal(t, pmetric.MetricTypeGauge, temp.Type())
	dp := dpByID(t, temp.Gauge().DataPoints(), "nvme/temp1")
	assert.Equal(t, pmetric.NumberDataPointValueTypeDouble, dp.ValueType())
	assert.InDelta(t, 40.5, dp.DoubleValue(), 1e-9)
	assert.Equal(t, pcommon.NewTimestampFromTime(now), dp.Timestamp())
	name, _ := dp.Attributes().Get("hw.name")
	parent, _ := dp.Attributes().Get("hw.parent")
	assert.Equal(t, "Composite", name.Str())
	assert.Equal(t, "nvme", parent.Str())

	fan := metricByName(t, md, "hw.fan.speed")
	assert.Equal(t, "rpm", fan.Unit())
	fdp := dpByID(t, fan.Gauge().DataPoints(), "nct6775/fan1")
	assert.Equal(t, pmetric.NumberDataPointValueTypeInt, fdp.ValueType())
	assert.Equal(t, int64(1200), fdp.IntValue())

	volt := metricByName(t, md, "hw.voltage")
	assert.Equal(t, "V", volt.Unit())
	assert.InDelta(t, 1.2, dpByID(t, volt.Gauge().DataPoints(), "nct6775/in0").DoubleValue(), 1e-9)
}

func TestScrapeEmitsTheLimitsWithTheirType(t *testing.T) {
	s := newScraper(zap.NewNop(), testConfig(), &fakeSource{readings: sampleReadings()})

	md := s.scrape(time.Now())

	lim := metricByName(t, md, "hw.temperature.limit")
	assert.Equal(t, "Cel", lim.Unit())
	dps := lim.Gauge().DataPoints()
	require.Equal(t, 2, dps.Len())
	got := map[string]float64{}
	for i := 0; i < dps.Len(); i++ {
		typ, ok := dps.At(i).Attributes().Get("hw.limit_type")
		require.True(t, ok)
		id, _ := dps.At(i).Attributes().Get("hw.id")
		assert.Equal(t, "nvme/temp1", id.Str())
		got[typ.Str()] = dps.At(i).DoubleValue()
	}
	assert.Equal(t, map[string]float64{"max": 84.85, "critical": 89.85}, got)
}

func TestScrapePassesTheSwitchesToTheSource(t *testing.T) {
	src := &fakeSource{readings: sampleReadings()}
	cfg := testConfig()
	cfg.CPUCores = true
	cfg.Fans = false
	s := newScraper(zap.NewNop(), cfg, src)

	s.scrape(time.Now())

	assert.Equal(t, readOptions{limits: true, fans: false, voltages: true, thermalZones: true, cpuCores: true}, src.opts)
}

func TestScrapeIncludeKeepsOnlyMatchingChipAndLabel(t *testing.T) {
	cfg := testConfig()
	cfg.Include = []string{"nvme:*"}
	s := newScraper(zap.NewNop(), cfg, &fakeSource{readings: sampleReadings()})

	md := s.scrape(time.Now())

	assert.Equal(t, 2, md.MetricCount()) // hw.temperature and its limit
	temp := metricByName(t, md, "hw.temperature")
	require.Equal(t, 1, temp.Gauge().DataPoints().Len())
	dpByID(t, temp.Gauge().DataPoints(), "nvme/temp1")
}

func TestScrapeExcludeRemovesANamedSensor(t *testing.T) {
	cfg := testConfig()
	cfg.Exclude = []string{"coretemp:Package id 0", "nct6775:*"}
	s := newScraper(zap.NewNop(), cfg, &fakeSource{readings: sampleReadings()})

	md := s.scrape(time.Now())

	temp := metricByName(t, md, "hw.temperature")
	require.Equal(t, 1, temp.Gauge().DataPoints().Len())
	dpByID(t, temp.Gauge().DataPoints(), "nvme/temp1")
	assert.Equal(t, 2, md.MetricCount())
}

func TestScrapeExcludeWinsOverInclude(t *testing.T) {
	cfg := testConfig()
	cfg.Include = []string{"nvme:*"}
	cfg.Exclude = []string{"nvme:Composite"}
	s := newScraper(zap.NewNop(), cfg, &fakeSource{readings: sampleReadings()})

	assert.Zero(t, s.scrape(time.Now()).MetricCount())
}

func TestScrapeWithNoSensorsLogsOneInfoLine(t *testing.T) {
	core, logs := observer.New(zapcore.InfoLevel)
	s := newScraper(zap.New(core), testConfig(), &fakeSource{err: errNoSensors})

	for range 3 {
		assert.Zero(t, s.scrape(time.Now()).MetricCount())
	}

	assert.Equal(t, 1, logs.FilterLevelExact(zapcore.InfoLevel).Len())
	assert.Zero(t, logs.FilterLevelExact(zapcore.WarnLevel).Len())
}

func TestScrapeSensorsReturningLogsInfoAgainAfterTheyDisappear(t *testing.T) {
	core, logs := observer.New(zapcore.InfoLevel)
	src := &fakeSource{err: errNoSensors}
	s := newScraper(zap.New(core), testConfig(), src)

	s.scrape(time.Now())
	src.err, src.readings = nil, sampleReadings()
	assert.NotZero(t, s.scrape(time.Now()).MetricCount())
	src.err, src.readings = errNoSensors, nil
	s.scrape(time.Now())

	assert.Equal(t, 2, logs.FilterLevelExact(zapcore.InfoLevel).Len())
}

func TestScrapeOtherErrorWarnsOncePerEpisode(t *testing.T) {
	core, logs := observer.New(zapcore.InfoLevel)
	src := &fakeSource{err: errors.New("boom")}
	s := newScraper(zap.New(core), testConfig(), src)

	for range 3 {
		assert.Zero(t, s.scrape(time.Now()).MetricCount())
	}
	src.err, src.readings = nil, sampleReadings()
	s.scrape(time.Now())
	src.err, src.readings = errors.New("boom"), nil
	s.scrape(time.Now())

	assert.Equal(t, 2, logs.FilterLevelExact(zapcore.WarnLevel).Len())
}
