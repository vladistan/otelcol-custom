package collect

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/vladistan/otelcol-custom/receiver/gpureceiver/internal/fakeport"
	"github.com/vladistan/otelcol-custom/receiver/gpureceiver/internal/port"
	"github.com/vladistan/otelcol-custom/receiver/gpureceiver/internal/semconv"
)

var (
	t0           = time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	identityKeys = []string{
		semconv.AttrID, semconv.AttrName, semconv.AttrModel,
		semconv.AttrType, semconv.AttrVendor, semconv.AttrDriverVersion,
	}
)

const gib = 1 << 30

// point is one flattened data point with its metric's shape.
type point struct {
	name      string
	unit      string
	typ       pmetric.MetricType
	monotonic bool
	start     pcommon.Timestamp
	ts        pcommon.Timestamp
	attrs     map[string]string
	isInt     bool
	intVal    int64
	floatVal  float64
}

func flatten(md pmetric.Metrics) []point {
	var out []point
	for r := range md.ResourceMetrics().Len() {
		scopes := md.ResourceMetrics().At(r).ScopeMetrics()
		for s := range scopes.Len() {
			metrics := scopes.At(s).Metrics()
			for m := range metrics.Len() {
				out = append(out, metricPoints(metrics.At(m))...)
			}
		}
	}
	return out
}

func metricPoints(m pmetric.Metric) []point {
	var dps pmetric.NumberDataPointSlice
	monotonic := false
	switch m.Type() {
	case pmetric.MetricTypeGauge:
		dps = m.Gauge().DataPoints()
	case pmetric.MetricTypeSum:
		dps = m.Sum().DataPoints()
		monotonic = m.Sum().IsMonotonic()
	default:
		return nil
	}
	var out []point
	for i := range dps.Len() {
		dp := dps.At(i)
		attrs := map[string]string{}
		dp.Attributes().Range(func(k string, v pcommon.Value) bool {
			attrs[k] = v.AsString()
			return true
		})
		out = append(out, point{
			name: m.Name(), unit: m.Unit(), typ: m.Type(), monotonic: monotonic,
			start: dp.StartTimestamp(), ts: dp.Timestamp(), attrs: attrs,
			isInt:  dp.ValueType() == pmetric.NumberDataPointValueTypeInt,
			intVal: dp.IntValue(), floatVal: dp.DoubleValue(),
		})
	}
	return out
}

func named(points []point, name string) []point {
	var out []point
	for _, p := range points {
		if p.name == name {
			out = append(out, p)
		}
	}
	return out
}

// pickAll returns the points of metric name whose attrs include every key=value pair.
func pickAll(points []point, name string, kv ...string) []point {
	var out []point
outer:
	for _, p := range named(points, name) {
		for i := 0; i < len(kv); i += 2 {
			if p.attrs[kv[i]] != kv[i+1] {
				continue outer
			}
		}
		out = append(out, p)
	}
	return out
}

// pick returns the single point matching name and the key=value pairs.
func pick(t *testing.T, points []point, name string, kv ...string) point {
	t.Helper()
	match := pickAll(points, name, kv...)
	require.Len(t, match, 1, "metric %s %v", name, kv)
	return match[0]
}

func keysOf(attrs map[string]string) []string {
	keys := make([]string, 0, len(attrs))
	for k := range attrs {
		keys = append(keys, k)
	}
	return keys
}

func (p point) value() float64 {
	if p.isInt {
		return float64(p.intVal)
	}
	return p.floatVal
}

// fullDevice scripts every call with steady, plausible readings.
func fullDevice(p *fakeport.Port, uuid, name string) *fakeport.Device {
	return p.AddDevice(uuid, name).
		Set(fakeport.CallUtilization, port.Utilization{GPU: 75, Memory: 50}).
		Set(fakeport.CallEncoderUtil, uint32(30)).
		Set(fakeport.CallDecoderUtil, uint32(20)).
		Set(fakeport.CallMemoryInfo, port.MemoryInfo{Used: 40 * gib, Total: 80 * gib}).
		Set(fakeport.CallPCIeReceive, uint32(1000)).
		Set(fakeport.CallPCIeTransmit, uint32(2000)).
		Set(fakeport.CallECCCorrected, uint64(5)).
		Set(fakeport.CallECCUncorrected, uint64(1)).
		Set(fakeport.CallTemperature, uint32(62)).
		Set(fakeport.CallPowerUsage, uint32(285000)).
		Set(fakeport.CallEnforcedPowerLimit, uint32(400000)).
		Set(fakeport.CallClockGraphics, uint32(1410)).
		Set(fakeport.CallClockSM, uint32(1410)).
		Set(fakeport.CallClockMemory, uint32(1593)).
		Set(fakeport.CallClockVideo, uint32(1200))
}

// identities enumerates the port the way the scraper does.
func identities(t *testing.T, p port.Port) []port.DeviceIdentity {
	t.Helper()
	version, err := p.DriverVersion()
	require.NoError(t, err)
	count, err := p.DeviceCount()
	require.NoError(t, err)
	var ids []port.DeviceIdentity
	for i := range count {
		dev, err := p.Device(i)
		require.NoError(t, err)
		uuid, err := dev.UUID()
		require.NoError(t, err)
		model, err := dev.Name()
		require.NoError(t, err)
		ids = append(ids, port.DeviceIdentity{
			Index: i, UUID: uuid, Model: model,
			Name: fmt.Sprintf("gpu%d", i), DriverVersion: version,
		})
	}
	return ids
}

type fixture struct {
	port *fakeport.Port
	c    *Collector
	logs *observer.ObservedLogs
}

func newFixture(t *testing.T, p *fakeport.Port, toggles port.Toggles, opts ...Option) *fixture {
	t.Helper()
	return newFixtureWithIDs(p, identities(t, p), toggles, opts...)
}

func newFixtureWithIDs(
	p *fakeport.Port, ids []port.DeviceIdentity, toggles port.Toggles, opts ...Option,
) *fixture {
	core, logs := observer.New(zapcore.DebugLevel)
	c := New(p, ids, toggles, t0, zap.New(core), opts...)
	return &fixture{port: p, c: c, logs: logs}
}

func staticIdentity(uuid string) []port.DeviceIdentity {
	return []port.DeviceIdentity{{Index: 0, UUID: uuid, Model: "A100", Name: "gpu0"}}
}

// oneGPU returns a fixture with a single fully scripted GPU.
func oneGPU(t *testing.T, toggles port.Toggles) (*fixture, *fakeport.Device) {
	t.Helper()
	p := fakeport.New().SetDriverVersion("550.54")
	dev := fullDevice(p, "GPU-0", "A100")
	return newFixture(t, p, toggles), dev
}

func (f *fixture) collect(at time.Time) []point {
	return flatten(f.c.Collect(at))
}

func (f *fixture) logsAt(level zapcore.Level) []observer.LoggedEntry {
	return f.logs.FilterLevelExact(level).All()
}

func allOff(names ...string) port.Toggles {
	toggles := port.Toggles{}
	for _, name := range names {
		toggles[name] = false
	}
	return toggles
}
