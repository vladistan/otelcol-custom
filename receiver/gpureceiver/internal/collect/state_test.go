package collect

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zapcore"

	"github.com/vladistan/otelcol-custom/receiver/gpureceiver/internal/fakeport"
	"github.com/vladistan/otelcol-custom/receiver/gpureceiver/internal/port"
	"github.com/vladistan/otelcol-custom/receiver/gpureceiver/internal/semconv"
)

func status(t *testing.T, points []point, uuid string) map[string]int64 {
	t.Helper()
	got := map[string]int64{}
	for _, p := range named(points, semconv.MetricStatus) {
		if p.attrs[semconv.AttrID] == uuid {
			got[p.attrs[semconv.AttrState]] = p.intVal
		}
	}
	return got
}

func twoGPUs(t *testing.T, toggles port.Toggles) (*fixture, *fakeport.Device, *fakeport.Device) {
	t.Helper()
	p := fakeport.New().SetDriverVersion("550.54")
	first := fullDevice(p, "GPU-0", "A100")
	second := fullDevice(p, "GPU-1", "A100")
	return newFixture(t, p, toggles), first, second
}

// Two GPUs give two series per metric, distinct hw.id.
func TestTwoGPUDistinctSeriesAndIdentity(t *testing.T) {
	f, _, _ := twoGPUs(t, nil)

	points := f.collect(t0)

	for _, row := range semconvRows {
		t.Run(row.name, func(t *testing.T) {
			got := named(points, row.name)
			assert.Len(t, got, row.points*2)
			ids := map[string]bool{}
			for _, p := range got {
				ids[p.attrs[semconv.AttrID]] = true
				for _, key := range identityKeys {
					assert.Contains(t, p.attrs, key)
				}
			}
			assert.Equal(t, map[string]bool{"GPU-0": true, "GPU-1": true}, ids)
		})
	}
}

func TestTwoGPUTemperaturePointsUseSensorIdentity(t *testing.T) {
	f, _, _ := twoGPUs(t, nil)

	points := named(f.collect(t0), semconv.MetricTemperature)

	require.Len(t, points, 2)
	assert.Equal(t, "GPU-0:temperature", points[0].attrs[semconv.AttrID])
	assert.Equal(t, "GPU-1", points[1].attrs[semconv.AttrParent])
	assert.Equal(t, "gpu1 temperature", points[1].attrs[semconv.AttrName])
	assert.Len(t, points[0].attrs, 4)
}

// NOT_SUPPORTED omits only that metric, once-logged, never re-asked.
func TestNotSupportedOmitsOnlyThatMetricAndLogsOnce(t *testing.T) {
	p := fakeport.New()
	fullDevice(p, "GPU-0", "A100").Queue(fakeport.CallPowerUsage, fakeport.Fail(port.NotSupported))
	f := newFixture(t, p, nil)

	var scrapes [][]point
	for i := range 3 {
		scrapes = append(scrapes, f.collect(t0.Add(time.Duration(i)*10*time.Second)))
	}

	for _, points := range scrapes {
		assert.Empty(t, named(points, semconv.MetricPower))
		assert.NotEmpty(t, named(points, semconv.MetricTemperature))
		assert.NotEmpty(t, named(points, semconv.MetricPowerLimit))
		assert.Equal(t, int64(1), status(t, points, "GPU-0")["ok"])
	}
	assert.Len(t, f.logsAt(zapcore.InfoLevel), 1)
	assert.Empty(t, f.logsAt(zapcore.WarnLevel))
	dev, err := p.Device(0)
	require.NoError(t, err)
	assert.Equal(t, 1, dev.(*fakeport.Device).Count(fakeport.CallPowerUsage))
}

func TestNotSupportedIsRememberedPerGPU(t *testing.T) {
	p := fakeport.New()
	fullDevice(p, "GPU-0", "A100").Set(fakeport.CallPowerUsage, uint32(100000)).
		Queue(fakeport.CallPowerUsage, fakeport.Fail(port.NotSupported))
	fullDevice(p, "GPU-1", "A100")
	f := newFixture(t, p, nil)

	points := f.collect(t0)

	power := named(points, semconv.MetricPower)
	require.Len(t, power, 1)
	assert.Equal(t, "GPU-1", power[0].attrs[semconv.AttrID])
}

// Points collected before the loss are kept; then only hw.status.
func TestLostKeepsCollectedPointsThenOnlyStatus(t *testing.T) {
	f, dev := oneGPU(t, nil)
	dev.Queue(fakeport.CallTemperature, fakeport.Fail(port.GPULost))

	first := f.collect(t0)

	assert.NotEmpty(t, named(first, semconv.MetricUtilization))
	assert.NotEmpty(t, named(first, semconv.MetricMemoryUsage))
	assert.NotEmpty(t, named(first, semconv.MetricErrors))
	assert.Empty(t, named(first, semconv.MetricTemperature))
	assert.Empty(t, named(first, semconv.MetricPower))
	assert.Empty(t, named(first, semconv.MetricClock))
	assert.Equal(t, map[string]int64{"ok": 0, "degraded": 0, "failed": 1}, status(t, first, "GPU-0"))

	second := f.collect(t0.Add(10 * time.Second))

	assert.Len(t, second, 3)
	assert.Equal(t, map[string]int64{"ok": 0, "degraded": 0, "failed": 1}, status(t, second, "GPU-0"))
	assert.Equal(t, 1, dev.Count(fakeport.CallTemperature))
	assert.Zero(t, dev.Count(fakeport.CallPowerUsage))
}

func TestLostWarnsOncePerGPU(t *testing.T) {
	f, dev := oneGPU(t, nil)
	dev.Queue(fakeport.CallTemperature, fakeport.Fail(port.GPULost))

	for i := range 3 {
		f.collect(t0.Add(time.Duration(i) * 10 * time.Second))
	}

	assert.Len(t, f.logsAt(zapcore.WarnLevel), 1)
}

func TestLostGPUDoesNotAffectOtherGPUs(t *testing.T) {
	f, first, _ := twoGPUs(t, nil)
	first.Queue(fakeport.CallUtilization, fakeport.Fail(port.GPULost))

	points := f.collect(t0)

	assert.Equal(t, int64(1), status(t, points, "GPU-0")["failed"])
	assert.Equal(t, int64(1), status(t, points, "GPU-1")["ok"])
	power := named(points, semconv.MetricPower)
	require.Len(t, power, 1)
	assert.Equal(t, "GPU-1", power[0].attrs[semconv.AttrID])
}

func TestLostWithStatusDisabledEmitsNothing(t *testing.T) {
	f, dev := oneGPU(t, allOff(semconv.MetricStatus))
	dev.Queue(fakeport.CallUtilization, fakeport.Fail(port.GPULost))
	f.collect(t0)

	md := f.c.Collect(t0.Add(10 * time.Second))

	assert.Zero(t, md.ResourceMetrics().Len())
}

func TestLostDeviceHandleLookupMarksGPULost(t *testing.T) {
	p := fakeport.New().FailDevice(0, port.GPULost)
	fullDevice(p, "GPU-0", "A100")
	f := newFixtureWithIDs(p, staticIdentity("GPU-0"), nil)

	points := f.collect(t0)

	assert.Equal(t, map[string]int64{"ok": 0, "degraded": 0, "failed": 1}, status(t, points, "GPU-0"))
}

func TestStatusOkWhenAnyEnabledCallSucceeds(t *testing.T) {
	p := fakeport.New()
	p.AddDevice("GPU-0", "A100").Set(fakeport.CallTemperature, uint32(60))
	f := newFixture(t, p, nil)

	points := f.collect(t0)

	assert.Equal(t, map[string]int64{"ok": 1, "degraded": 0, "failed": 0}, status(t, points, "GPU-0"))
}

func TestStatusUnknownWhenEveryCallFailsTransiently(t *testing.T) {
	p := fakeport.New()
	dev := p.AddDevice("GPU-0", "A100")
	for _, call := range []fakeport.Call{
		fakeport.CallUtilization, fakeport.CallEncoderUtil, fakeport.CallDecoderUtil, fakeport.CallMemoryInfo,
		fakeport.CallPCIeReceive, fakeport.CallPCIeTransmit, fakeport.CallECCCorrected, fakeport.CallECCUncorrected,
		fakeport.CallTemperature, fakeport.CallPowerUsage, fakeport.CallEnforcedPowerLimit,
		fakeport.CallClockGraphics, fakeport.CallClockSM, fakeport.CallClockMemory, fakeport.CallClockVideo,
	} {
		dev.Queue(call, fakeport.Fail(port.Other))
	}
	f := newFixture(t, p, nil)

	points := f.collect(t0)

	assert.Equal(t, map[string]int64{"ok": 0, "degraded": 0, "failed": 0}, status(t, points, "GPU-0"))
	assert.Len(t, points, 3)
}

func TestStatusUnknownWhenNothingIsEnabledButStatus(t *testing.T) {
	f, _ := oneGPU(t, allOff(
		semconv.MetricUtilization, semconv.MetricMemoryUsage, semconv.MetricMemoryLimit,
		semconv.MetricMemoryUtilization, semconv.MetricIO, semconv.MetricErrors, semconv.MetricTemperature,
		semconv.MetricPower, semconv.MetricClock, semconv.MetricPowerLimit, semconv.MetricPCIeThroughput,
	))

	points := f.collect(t0)

	assert.Equal(t, map[string]int64{"ok": 0, "degraded": 0, "failed": 0}, status(t, points, "GPU-0"))
}

func TestStatusDisabledEmitsNoStatusPoints(t *testing.T) {
	f, _ := oneGPU(t, allOff(semconv.MetricStatus))

	points := f.collect(t0)

	assert.Empty(t, named(points, semconv.MetricStatus))
	assert.NotEmpty(t, named(points, semconv.MetricPower))
}

func TestStatusDegradedIsAlwaysZero(t *testing.T) {
	f, _ := oneGPU(t, nil)

	assert.Equal(t, map[string]int64{"ok": 1, "degraded": 0, "failed": 0}, status(t, f.collect(t0), "GPU-0"))
}

// Transient errors skip the point and warn once per call and kind.
func TestTransientErrorWarnsOncePerKindThenDebug(t *testing.T) {
	f, dev := oneGPU(t, nil)
	dev.Queue(fakeport.CallPowerUsage,
		fakeport.Fail(port.Other), fakeport.Fail(port.Other), fakeport.Fail(port.NoPermission))

	first := f.collect(t0)
	for i := 1; i <= 2; i++ {
		f.collect(t0.Add(time.Duration(i) * 10 * time.Second))
	}

	assert.Empty(t, named(first, semconv.MetricPower))
	assert.Len(t, f.logsAt(zapcore.WarnLevel), 2)
	assert.Len(t, f.logsAt(zapcore.DebugLevel), 1)
	assert.Equal(t, int64(1), status(t, first, "GPU-0")["ok"])
}

func TestDeviceHandleFailureSkipsGPUWithUnknownStatus(t *testing.T) {
	p := fakeport.New().FailDevice(0, port.Other)
	fullDevice(p, "GPU-0", "A100")
	f := newFixtureWithIDs(p, staticIdentity("GPU-0"), nil)

	points := f.collect(t0)

	assert.Len(t, points, 3)
	assert.Equal(t, map[string]int64{"ok": 0, "degraded": 0, "failed": 0}, status(t, points, "GPU-0"))
	assert.Len(t, f.logsAt(zapcore.WarnLevel), 1)
}

func TestDeviceHandleIsCachedAfterFirstLookup(t *testing.T) {
	f, _ := oneGPU(t, nil)

	for i := range 3 {
		f.collect(t0.Add(time.Duration(i) * 10 * time.Second))
	}

	assert.Equal(t, 2, f.port.Calls("Device"), "one enumeration lookup plus one cached collector lookup")
}

func TestToggleDisabledMetricsSkipTheirPortCalls(t *testing.T) {
	f, dev := oneGPU(t, allOff(semconv.MetricTemperature, semconv.MetricPower, semconv.MetricClock))

	points := f.collect(t0)

	assert.Zero(t, dev.Count(fakeport.CallTemperature))
	assert.Zero(t, dev.Count(fakeport.CallPowerUsage))
	assert.Zero(t, dev.Count(fakeport.CallClockSM))
	assert.Empty(t, named(points, semconv.MetricTemperature))
	assert.NotEmpty(t, named(points, semconv.MetricPowerLimit))
}

func TestToggleUtilizationOffSkipsAllThreeTasks(t *testing.T) {
	f, dev := oneGPU(t, allOff(semconv.MetricUtilization))

	f.collect(t0)

	assert.Zero(t, dev.Count(fakeport.CallUtilization))
	assert.Zero(t, dev.Count(fakeport.CallEncoderUtil))
	assert.Zero(t, dev.Count(fakeport.CallDecoderUtil))
}

func TestToggleMemoryCallSkippedWhenAllThreeMetricsOff(t *testing.T) {
	f, dev := oneGPU(t, allOff(
		semconv.MetricMemoryUsage, semconv.MetricMemoryLimit, semconv.MetricMemoryUtilization))

	f.collect(t0)

	assert.Zero(t, dev.Count(fakeport.CallMemoryInfo))
}

func TestToggleMemoryCallRunsOnceWhenOneMetricIsOn(t *testing.T) {
	f, dev := oneGPU(t, allOff(semconv.MetricMemoryUsage, semconv.MetricMemoryLimit))

	points := f.collect(t0)

	assert.Equal(t, 1, dev.Count(fakeport.CallMemoryInfo))
	assert.Len(t, named(points, semconv.MetricMemoryUtilization), 1)
	assert.Empty(t, named(points, semconv.MetricMemoryUsage))
}

func TestPCIeSkippedOnlyWhenIoAndThroughputAreBothOff(t *testing.T) {
	tests := []struct {
		name           string
		off            []string
		wantCalls      int
		wantIo         int
		wantThroughput int
	}{
		{"both on", nil, 1, 2, 2},
		{"io off", []string{semconv.MetricIO}, 1, 0, 2},
		{"throughput off", []string{semconv.MetricPCIeThroughput}, 1, 2, 0},
		{"both off", []string{semconv.MetricIO, semconv.MetricPCIeThroughput}, 0, 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, dev := oneGPU(t, allOff(tt.off...))

			points := f.collect(t0)

			assert.Equal(t, tt.wantCalls, dev.Count(fakeport.CallPCIeReceive))
			assert.Equal(t, tt.wantCalls, dev.Count(fakeport.CallPCIeTransmit))
			assert.Len(t, named(points, semconv.MetricIO), tt.wantIo)
			assert.Len(t, named(points, semconv.MetricPCIeThroughput), tt.wantThroughput)
		})
	}
}

func TestToggleMissingKeyCountsAsEnabled(t *testing.T) {
	f, _ := oneGPU(t, port.Toggles{semconv.MetricPower: false})

	points := f.collect(t0)

	assert.Empty(t, named(points, semconv.MetricPower))
	assert.NotEmpty(t, named(points, semconv.MetricTemperature))
}

func TestCollectReturnsEmptyBatchWhenThereAreNoDevices(t *testing.T) {
	f := newFixture(t, fakeport.New(), nil)

	md := f.c.Collect(t0)

	assert.Zero(t, md.ResourceMetrics().Len())
	assert.Zero(t, md.DataPointCount())
}
