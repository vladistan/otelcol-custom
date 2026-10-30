package collect

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vladistan/otelcol-custom/receiver/gpureceiver/internal/fakeport"
	"github.com/vladistan/otelcol-custom/receiver/gpureceiver/internal/port"
	"github.com/vladistan/otelcol-custom/receiver/gpureceiver/internal/semconv"
)

const maxGap = 30 * time.Second

func TestEccIntegratorSequence(t *testing.T) {
	tests := []struct {
		name string
		raws []uint64
		want []uint64
	}{
		{"first sample is zero", []uint64{10}, []uint64{0}},
		{"rising counter reports the delta", []uint64{10, 12, 12, 20}, []uint64{0, 2, 2, 10}},
		{"drop adds the new raw value", []uint64{10, 12, 3, 7}, []uint64{0, 2, 5, 9}},
		{"repeated drops stay monotonic", []uint64{10, 12, 3, 7, 2}, []uint64{0, 2, 5, 9, 11}},
		{"drop to zero keeps the earlier total", []uint64{4, 9, 0, 0}, []uint64{0, 5, 5, 5}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var integrator eccIntegrator
			var got []uint64
			for _, raw := range tt.raws {
				got = append(got, integrator.sample(raw))
			}
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestIoIntegratorFirstSampleIsZero(t *testing.T) {
	var integrator ioIntegrator

	assert.Zero(t, integrator.sample(t0, 5000, maxGap))
}

func TestIoIntegratorUsesMeasuredElapsedTime(t *testing.T) {
	var integrator ioIntegrator
	integrator.sample(t0, 100, maxGap)

	afterFour := integrator.sample(t0.Add(4*time.Second), 100, maxGap)
	afterFourteen := integrator.sample(t0.Add(14*time.Second), 200, maxGap)

	assert.Equal(t, uint64(100*1024*4), afterFour)
	assert.Equal(t, uint64(100*1024*4+200*1024*10), afterFourteen)
}

func TestIoIntegratorLongGapRebaselines(t *testing.T) {
	var integrator ioIntegrator
	integrator.sample(t0, 1000, maxGap)
	integrator.sample(t0.Add(10*time.Second), 1000, maxGap)

	afterGap := integrator.sample(t0.Add(41*time.Second), 1000, maxGap)
	afterNext := integrator.sample(t0.Add(51*time.Second), 1000, maxGap)

	assert.Equal(t, uint64(1000*1024*10), afterGap)
	assert.Equal(t, uint64(1000*1024*20), afterNext)
}

func TestIoIntegratorGapAtThresholdStillCounts(t *testing.T) {
	var integrator ioIntegrator
	integrator.sample(t0, 1, maxGap)

	assert.Equal(t, uint64(1024*30), integrator.sample(t0.Add(maxGap), 1, maxGap))
}

func TestIoIntegratorEmitsTruncatedFloatTotal(t *testing.T) {
	var integrator ioIntegrator
	integrator.sample(t0, 1, maxGap)

	first := integrator.sample(t0.Add(1500*time.Microsecond), 1, maxGap)
	second := integrator.sample(t0.Add(3*time.Millisecond), 1, maxGap)

	assert.Equal(t, uint64(1), first)
	assert.Equal(t, uint64(3), second)
}

func TestIoIntegratorIgnoresClockGoingBackwards(t *testing.T) {
	var integrator ioIntegrator
	integrator.sample(t0, 1000, maxGap)

	assert.Zero(t, integrator.sample(t0.Add(-time.Second), 1000, maxGap))
}

func rx(t *testing.T, points []point) point {
	t.Helper()
	return pick(t, points, semconv.MetricIO, semconv.AttrNetworkDir, "receive")
}

func rxPoints(points []point) []point {
	return pickAll(points, semconv.MetricIO, semconv.AttrNetworkDir, "receive")
}

// Measured elapsed time, monotonic output.
func TestIoCollectorUsesMeasuredTimeBetweenUnevenScrapes(t *testing.T) {
	f, _ := oneGPU(t, nil)

	var totals []int64
	for _, offset := range []time.Duration{0, 4 * time.Second, 14 * time.Second} {
		totals = append(totals, rx(t, f.collect(t0.Add(offset))).intVal)
	}

	assert.Equal(t, []int64{0, 1000 * 1024 * 4, 1000 * 1024 * 14}, totals)
}

func TestIoCollectorFailedSampleHoldsTotalAndKeepsPreviousTime(t *testing.T) {
	f, dev := oneGPU(t, nil)
	dev.Queue(fakeport.CallPCIeReceive, fakeport.Ok(uint32(1000)), fakeport.Fail(port.Other))

	first := f.collect(t0)
	failed := f.collect(t0.Add(10 * time.Second))
	third := f.collect(t0.Add(20 * time.Second))

	assert.Zero(t, rx(t, first).intVal)
	assert.Empty(t, rxPoints(failed))
	assert.Len(t, named(failed, semconv.MetricIO), 1)
	assert.Equal(t, int64(1000*1024*20), rx(t, third).intVal)
}

func TestIoCollectorLongGapRebaselinesUsingConfiguredInterval(t *testing.T) {
	p := fakeport.New()
	fullDevice(p, "GPU-0", "A100")
	f := newFixture(t, p, nil, WithInterval(10*time.Second))

	f.collect(t0)
	afterGap := rx(t, f.collect(t0.Add(31*time.Second)))
	next := rx(t, f.collect(t0.Add(41*time.Second)))

	assert.Zero(t, afterGap.intVal)
	assert.Equal(t, int64(1000*1024*10), next.intVal)
}

func TestIoCollectorFirstSampleIsZeroForBothDirections(t *testing.T) {
	f, _ := oneGPU(t, nil)

	points := named(f.collect(t0), semconv.MetricIO)

	assert.Len(t, points, 2)
	for _, p := range points {
		assert.Zero(t, p.intVal)
	}
}

// Single-bit maps to corrected, double-bit to uncorrected.
func TestEccCollectorMapsKindsToErrorType(t *testing.T) {
	f, dev := oneGPU(t, nil)
	f.collect(t0)
	dev.Set(fakeport.CallECCCorrected, uint64(8)).Set(fakeport.CallECCUncorrected, uint64(2))

	points := f.collect(t0.Add(10 * time.Second))

	corrected := pick(t, points, semconv.MetricErrors, semconv.AttrErrorType, "corrected")
	uncorrected := pick(t, points, semconv.MetricErrors, semconv.AttrErrorType, "uncorrected")
	assert.Equal(t, int64(3), corrected.intVal)
	assert.Equal(t, int64(1), uncorrected.intVal)
}

func TestEccCollectorFirstSampleIsZeroAtReceiverStart(t *testing.T) {
	f, _ := oneGPU(t, nil)

	points := named(f.collect(t0.Add(10*time.Second)), semconv.MetricErrors)

	require.Len(t, points, 2)
	for _, p := range points {
		assert.Zero(t, p.intVal)
		assert.Equal(t, t0.UnixNano(), int64(p.start))
	}
}

func TestEccCollectorDriverReloadKeepsSeriesMonotonic(t *testing.T) {
	f, dev := oneGPU(t, nil)
	f.collect(t0)
	dev.Set(fakeport.CallECCCorrected, uint64(9))
	before := pick(t, f.collect(t0.Add(10*time.Second)), semconv.MetricErrors, semconv.AttrErrorType, "corrected")
	dev.Set(fakeport.CallECCCorrected, uint64(2))

	after := pick(t, f.collect(t0.Add(20*time.Second)), semconv.MetricErrors, semconv.AttrErrorType, "corrected")

	assert.Equal(t, int64(4), before.intVal)
	assert.Equal(t, int64(4+2), after.intVal)
}

func TestEccCollectorTransientErrorSkipsPointAndHoldsTotal(t *testing.T) {
	f, dev := oneGPU(t, nil)
	f.collect(t0)
	dev.Queue(fakeport.CallECCCorrected, fakeport.Fail(port.Other))
	skipped := f.collect(t0.Add(10 * time.Second))
	dev.Set(fakeport.CallECCCorrected, uint64(7))

	resumed := pick(t, f.collect(t0.Add(20*time.Second)), semconv.MetricErrors, semconv.AttrErrorType, "corrected")

	assert.Len(t, named(skipped, semconv.MetricErrors), 1)
	assert.Equal(t, int64(2), resumed.intVal)
}
