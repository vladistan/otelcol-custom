package collect

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/pmetric"

	"github.com/vladistan/otelcol-custom/receiver/gpureceiver/internal/fakeport"
	"github.com/vladistan/otelcol-custom/receiver/gpureceiver/internal/port"
	"github.com/vladistan/otelcol-custom/receiver/gpureceiver/internal/semconv"
)

type schemaRow struct {
	name      string
	unit      string
	typ       pmetric.MetricType
	monotonic bool
	extraKeys []string
	points    int
}

// The nine semconv metrics match semconv v1.44.0.
var semconvRows = []schemaRow{
	{semconv.MetricUtilization, "1", pmetric.MetricTypeGauge, false, []string{semconv.AttrGPUTask}, 3},
	{semconv.MetricMemoryUsage, "By", pmetric.MetricTypeSum, false, nil, 1},
	{semconv.MetricMemoryLimit, "By", pmetric.MetricTypeSum, false, nil, 1},
	{semconv.MetricMemoryUtilization, "1", pmetric.MetricTypeGauge, false, nil, 1},
	{semconv.MetricIO, "By", pmetric.MetricTypeSum, true, []string{semconv.AttrNetworkDir}, 2},
	{semconv.MetricErrors, "{error}", pmetric.MetricTypeSum, true, []string{semconv.AttrErrorType}, 2},
	{semconv.MetricStatus, "1", pmetric.MetricTypeSum, false, []string{semconv.AttrState}, 3},
	{semconv.MetricPower, "W", pmetric.MetricTypeGauge, false, nil, 1},
}

// The three extensions are asserted separately from semconv.
var extensionRows = []schemaRow{
	{semconv.MetricClock, "Hz", pmetric.MetricTypeGauge, false, []string{semconv.AttrClockDomain}, 4},
	{semconv.MetricPowerLimit, "W", pmetric.MetricTypeGauge, false, nil, 1},
	{semconv.MetricPCIeThroughput, "By/s", pmetric.MetricTypeGauge, false, []string{semconv.AttrNetworkDir}, 2},
}

func assertSchemaRow(t *testing.T, points []point, row schemaRow) {
	t.Helper()
	got := named(points, row.name)
	require.Len(t, got, row.points)
	want := append(append([]string{}, identityKeys...), row.extraKeys...)
	for _, p := range got {
		assert.Equal(t, row.unit, p.unit)
		assert.Equal(t, row.typ, p.typ)
		assert.Equal(t, row.monotonic, p.monotonic)
		assert.ElementsMatch(t, want, keysOf(p.attrs))
	}
}

func TestSemconvMetricsMatchSchema(t *testing.T) {
	f, _ := oneGPU(t, nil)
	points := f.collect(t0)

	for _, row := range semconvRows {
		t.Run(row.name, func(t *testing.T) { assertSchemaRow(t, points, row) })
	}
}

func TestSemconvTemperatureCarriesOnlySensorAttributes(t *testing.T) {
	f, _ := oneGPU(t, nil)

	temp := pick(t, f.collect(t0), semconv.MetricTemperature)

	assert.Equal(t, "Cel", temp.unit)
	assert.Equal(t, pmetric.MetricTypeGauge, temp.typ)
	assert.Equal(t, map[string]string{
		semconv.AttrID:             "GPU-0:temperature",
		semconv.AttrParent:         "GPU-0",
		semconv.AttrName:           "gpu0 temperature",
		semconv.AttrSensorLocation: "gpu",
	}, temp.attrs)
}

func TestExtensionMetricsMatchSchema(t *testing.T) {
	f, _ := oneGPU(t, nil)
	points := f.collect(t0)

	for _, row := range extensionRows {
		t.Run(row.name, func(t *testing.T) { assertSchemaRow(t, points, row) })
	}
}

func TestSemconvIdentityValues(t *testing.T) {
	f, _ := oneGPU(t, nil)

	power := pick(t, f.collect(t0), semconv.MetricPower)

	assert.Equal(t, map[string]string{
		semconv.AttrID:            "GPU-0",
		semconv.AttrName:          "gpu0",
		semconv.AttrModel:         "A100",
		semconv.AttrType:          "gpu",
		semconv.AttrVendor:        "nvidia",
		semconv.AttrDriverVersion: "550.54",
	}, power.attrs)
}

func TestSemconvIdentityOmitsEmptyDriverVersion(t *testing.T) {
	p := fakeport.New()
	fullDevice(p, "GPU-0", "A100")
	f := newFixtureWithIDs(p, staticIdentity("GPU-0"), nil)

	power := pick(t, f.collect(t0), semconv.MetricPower)

	assert.NotContains(t, power.attrs, semconv.AttrDriverVersion)
}

func TestSemconvUnitConversions(t *testing.T) {
	f, _ := oneGPU(t, nil)
	points := f.collect(t0)

	tests := []struct {
		name string
		kv   []string
		want float64
	}{
		{semconv.MetricUtilization, []string{semconv.AttrGPUTask, "general"}, 0.75},
		{semconv.MetricUtilization, []string{semconv.AttrGPUTask, "encoder"}, 0.30},
		{semconv.MetricUtilization, []string{semconv.AttrGPUTask, "decoder"}, 0.20},
		{semconv.MetricMemoryUsage, nil, 40 * gib},
		{semconv.MetricMemoryLimit, nil, 80 * gib},
		{semconv.MetricMemoryUtilization, nil, 0.5},
		{semconv.MetricTemperature, nil, 62},
		{semconv.MetricPower, nil, 285},
		{semconv.MetricPowerLimit, nil, 400},
		{semconv.MetricPCIeThroughput, []string{semconv.AttrNetworkDir, "receive"}, 1000 * 1024},
		{semconv.MetricPCIeThroughput, []string{semconv.AttrNetworkDir, "transmit"}, 2000 * 1024},
		{semconv.MetricClock, []string{semconv.AttrClockDomain, "graphics"}, 1410e6},
		{semconv.MetricClock, []string{semconv.AttrClockDomain, "memory"}, 1593e6},
		{semconv.MetricClock, []string{semconv.AttrClockDomain, "video"}, 1200e6},
	}
	for _, tt := range tests {
		t.Run(tt.name+fmtKV(tt.kv), func(t *testing.T) {
			assert.InDelta(t, tt.want, pick(t, points, tt.name, tt.kv...).value(), 1e-9)
		})
	}
}

func fmtKV(kv []string) string {
	if len(kv) == 0 {
		return ""
	}
	return "/" + kv[1]
}

func TestSemconvCountersCarryReceiverStartAndGaugesDoNot(t *testing.T) {
	f, _ := oneGPU(t, nil)
	points := f.collect(t0.Add(10_000_000_000))

	io := pick(t, points, semconv.MetricIO, semconv.AttrNetworkDir, "receive")
	util := pick(t, points, semconv.MetricUtilization, semconv.AttrGPUTask, "general")

	assert.Equal(t, t0.UnixNano(), int64(io.start))
	assert.Equal(t, t0.Add(10_000_000_000).UnixNano(), int64(io.ts))
	assert.Zero(t, util.start)
}

func TestSemconvBatchHasOneEmptyResourceAndOneScope(t *testing.T) {
	f, _ := oneGPU(t, nil)

	md := f.c.Collect(t0)

	require.Equal(t, 1, md.ResourceMetrics().Len())
	rm := md.ResourceMetrics().At(0)
	assert.Zero(t, rm.Resource().Attributes().Len())
	require.Equal(t, 1, rm.ScopeMetrics().Len())
	assert.Equal(t, ScopeName, rm.ScopeMetrics().At(0).Scope().Name())
}

func TestSemconvMemoryUtilizationSkippedWhenTotalIsZero(t *testing.T) {
	f, dev := oneGPU(t, nil)
	dev.Set(fakeport.CallMemoryInfo, port.MemoryInfo{})

	points := f.collect(t0)

	assert.Empty(t, named(points, semconv.MetricMemoryUtilization))
	assert.Len(t, named(points, semconv.MetricMemoryUsage), 1)
}

func TestSemconvToggleKeysMatchSchemaRows(t *testing.T) {
	var names []string
	for _, row := range append(append([]schemaRow{}, semconvRows...), extensionRows...) {
		names = append(names, row.name)
	}
	names = append(names, semconv.MetricTemperature)

	assert.ElementsMatch(t, names, semconv.ToggleKeys())
}
