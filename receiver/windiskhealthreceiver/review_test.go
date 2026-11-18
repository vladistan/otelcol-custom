// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package windiskhealthreceiver

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component/componenttest"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.uber.org/zap/zapcore"

	"github.com/vladistan/otelcol-custom/receiver/hwschema"
	"github.com/vladistan/otelcol-custom/receiver/smartdecode"
	"github.com/vladistan/otelcol-custom/receiver/smartdecode/smarttest"
)

func TestDecodeTemperature_NotReportedValue_ReturnsUnsupportedOnlyFor0x8000(t *testing.T) {
	out := make([]byte, temperatureHeader+temperatureInfoLen)
	littleEndian.PutUint16(out[12:], 1)
	littleEndian.PutUint16(out[temperatureHeader+2:], 0x8000)
	_, err := decodeTemperature(out)
	assert.ErrorIs(t, err, smartdecode.ErrUnsupported)

	littleEndian.PutUint16(out[temperatureHeader+2:], 0x8001)
	got, err := decodeTemperature(out)
	require.NoError(t, err)
	assert.Equal(t, -32767, got, "only 0x8000 means not reported")
}

func TestSessionTemperature_NotReportedValue_ReturnsUnsupported(t *testing.T) {
	temp := make([]byte, temperatureHeader+temperatureInfoLen)
	littleEndian.PutUint16(temp[12:], 1)
	littleEndian.PutUint16(temp[temperatureHeader+2:], 0x8000)
	s := newSession(&fakeController{replies: [][]byte{temp}})
	_, err := s.temperature()
	assert.ErrorIs(t, err, smartdecode.ErrUnsupported)
}

func TestReceiver_TheLoopOutlivesTheContextOfStart(t *testing.T) {
	b := newFakeBus()
	b.add("PhysicalDrive0", sataDisk(smarttest.HealthyATA("SN1")))
	sink := new(consumertest.MetricsSink)
	r := newTestReceiver(t, b, sink, 50*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	require.NoError(t, r.Start(ctx, componenttest.NewNopHost()))
	cancel()

	require.Eventually(t, func() bool { return len(sink.AllMetrics()) > 0 }, 3*time.Second, 10*time.Millisecond)
	require.NoError(t, r.Shutdown(context.Background()))
}

func TestDecodeDescriptor_AnOffsetOfAllOnesIsAbsent(t *testing.T) {
	buf := descriptorBuffer(11, "", "ACME", "", "")
	littleEndian.PutUint32(buf[24:], 0xFFFFFFFF)
	littleEndian.PutUint32(buf[20:], 0xFFFFFFFF)
	d, err := decodeDescriptor(buf)
	require.NoError(t, err)
	assert.Empty(t, d.serial)
	assert.Empty(t, d.firmware)
	assert.Equal(t, "ACME", d.product)
}

type fakeCall struct {
	results []waitResult
	waits   []time.Duration
	cancels int
}

type waitResult struct {
	done bool
	err  error
}

func (f *fakeCall) wait(d time.Duration) (bool, error) {
	f.waits = append(f.waits, d)
	r := f.results[0]
	f.results = f.results[1:]
	return r.done, r.err
}

func (f *fakeCall) cancel() { f.cancels++ }

const (
	testTimeout = 5 * time.Second
	testGrace   = time.Second
)

func TestAwaitCall_CallDoneInTime_FinishesWithoutCancel(t *testing.T) {
	call := &fakeCall{results: []waitResult{{done: true}}}
	finished, err := awaitCall(call, testTimeout, testGrace)
	assert.True(t, finished)
	assert.NoError(t, err)
	assert.Zero(t, call.cancels)
}

func TestAwaitCall_CancelLandsInGrace_FinishesWithTimeout(t *testing.T) {
	call := &fakeCall{results: []waitResult{{}, {done: true}}}
	finished, err := awaitCall(call, testTimeout, testGrace)
	assert.True(t, finished, "the cancel was seen to finish")
	assert.ErrorIs(t, err, smartdecode.ErrTimeout)
	assert.Equal(t, 1, call.cancels)
	assert.Equal(t, []time.Duration{testTimeout, testGrace}, call.waits, "the second wait is bounded")
}

func TestAwaitCall_DriverIgnoresCancel_ReportsNotFinished(t *testing.T) {
	call := &fakeCall{results: []waitResult{{}, {}}}
	finished, err := awaitCall(call, testTimeout, testGrace)
	assert.False(t, finished, "the driver ignored the cancel: the buffers must stay alive")
	assert.ErrorIs(t, err, smartdecode.ErrTimeout)
	assert.Equal(t, 1, call.cancels)
}

func TestAwaitCall_WaitFails_CancelsAndReturnsTheWaitError(t *testing.T) {
	call := &fakeCall{results: []waitResult{{err: errors.New("wait failed")}, {done: true}}}
	finished, err := awaitCall(call, testTimeout, testGrace)
	assert.True(t, finished)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "wait failed")
	assert.Equal(t, 1, call.cancels, "a failed wait still cancels the call")
}

func TestAwaitCall_WaitAfterCancelFails_ReportsNotFinished(t *testing.T) {
	call := &fakeCall{results: []waitResult{{}, {done: true, err: errors.New("grace wait failed")}}}
	finished, err := awaitCall(call, testTimeout, testGrace)
	assert.False(t, finished, "a failed grace wait proves nothing about the buffers")
	assert.ErrorIs(t, err, smartdecode.ErrTimeout)
}

func TestTimeoutMillis_SubMillisecond_ReturnsOne(t *testing.T) {
	assert.Equal(t, uint32(1), timeoutMillis(500*time.Microsecond), "never 0, which would not wait")
}

func TestTimeoutMillis_FiveSeconds_Returns5000(t *testing.T) {
	assert.Equal(t, uint32(5000), timeoutMillis(5*time.Second))
}

func TestTimeoutMillis_HugeDuration_StopsBelowInfinite(t *testing.T) {
	assert.Equal(t, uint32(math.MaxUint32-1), timeoutMillis(time.Duration(math.MaxInt64)))
}

func TestConfig_TheCommandTimeoutHasAFloor(t *testing.T) {
	cfg := createDefaultConfig().(*Config)
	cfg.CommandTimeout = 50 * time.Millisecond
	err := cfg.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least 100ms")
	cfg.CommandTimeout = 100 * time.Millisecond
	assert.NoError(t, cfg.Validate())
}

func TestScrape_AFailedSmartReadKeepsTheIdentityOfTheLastGoodRead(t *testing.T) {
	b := newFakeBus()
	d := b.add("PhysicalDrive0", sataDisk(smarttest.HealthyATA("SN1")))
	h := newHarness(t, b)

	md := h.scrape()
	for key := range points(md, hwschema.MetricTemperature) {
		assert.Contains(t, key, hwschema.AttrID+"=SN1,")
	}

	d.ata = &smarttest.ATA{FailWith: smartdecode.ErrUnsupported}
	md = h.scrape()
	temps := points(md, hwschema.MetricTemperature)
	require.Len(t, temps, 1)
	for key := range temps {
		assert.Contains(t, key, hwschema.AttrID+"=SN1,", "one disk, one series")
		assert.Contains(t, key, hwschema.AttrSerialNumber+"=SN1,")
	}
	for key := range points(md, hwschema.MetricStatus) {
		assert.Contains(t, key, hwschema.AttrID+"=SN1,")
	}
}

func TestScrape_ATimeoutOrRefusalAfterEarlierStepsKeepsWhatWasRead(t *testing.T) {
	for name, stop := range map[string]error{"timeout": smartdecode.ErrTimeout, "permission": smartdecode.ErrPermission} {
		b := newFakeBus()
		d := b.add("PhysicalDrive0", &fakeSession{desc: descriptor{busType: 11}, temp: 41, predErr: stop, ata: smarttest.HealthyATA("SN1")})
		h := newHarness(t, b)
		md := h.scrape()

		assert.Equal(t, 41.0, only(t, md, hwschema.MetricTemperature), name)
		assert.Equal(t, 3, d.sent, "%s: the descriptor, the temperature and the failed prediction only", name)
		assert.Equal(t, 1, int(sumValues(h.skipped(t))), name)
	}
}

func sumValues(m map[string]int64) int64 {
	var n int64
	for _, v := range m {
		n += v
	}
	return n
}

func TestScrape_StepsThatFailForDifferentReasonsLogOncePerEpisode(t *testing.T) {
	b := newFakeBus()
	d := b.add("PhysicalDrive0", sataDisk(smarttest.HealthyATA("SN1")))
	d.tempErr = fmt.Errorf("query the temperature: %w", smartdecode.ErrUnsupported)
	d.predErr = errors.New("device error")
	h := newHarness(t, b)

	for range 4 {
		h.scrape()
	}
	assert.Len(t, h.logged(zapcore.InfoLevel), 1, "the unsupported temperature logs once")
	assert.Len(t, h.logged(zapcore.WarnLevel), 1, "the failing prediction logs once")
	assert.Equal(t, map[string]int64{reasonUnsupported: 4, reasonError: 4}, h.skipped(t), "every failure is still counted")

	d.tempErr = nil
	h.scrape()
	d.tempErr = smartdecode.ErrUnsupported
	h.scrape()
	assert.Len(t, h.logged(zapcore.InfoLevel), 2, "a step that recovered starts a new episode when it fails again")
}

func TestMultiplySaturating_SmallFactors_ReturnsTheProduct(t *testing.T) {
	assert.Equal(t, uint64(6), multiplySaturating(2, 3))
	assert.Equal(t, uint64(0), multiplySaturating(0, math.MaxUint64))
}

func TestMultiplySaturating_ProductWraps_ReturnsMaxUint64(t *testing.T) {
	assert.Equal(t, uint64(math.MaxUint64), multiplySaturating(1<<40, 1<<40))
	assert.Equal(t, uint64(math.MaxUint64), multiplySaturating(math.MaxUint64, 2))
}

func TestScrape_ALargeDataMovedCounterGivesTheExactProduct(t *testing.T) {
	attrs := []smarttest.RawAttr{
		{ID: 9, Value: 98, Worst: 98, Raw: smarttest.RawNumber(100)},
		{ID: 241, Value: 100, Worst: 100, Raw: smarttest.RawNumber(1 << 47)},
	}
	fake := smarttest.HealthyATA("SN1")
	fake.Data = smarttest.SmartDataBuffer(attrs)
	fake.Thresholds = smarttest.ThresholdBuffer(attrs)
	b := newFakeBus()
	b.add("PhysicalDrive0", sataDisk(fake))

	md := newHarness(t, b).scrape()
	assert.Equal(t, float64(uint64(1)<<47*512), only(t, md, hwschema.MetricPhysicalDiskIO), "the largest 6-byte raw value times 512 does not wrap")
}

// The series of the Linux smart receiver and of this receiver, for the same fixtures, come from one list.
func seriesSignature(md pmetric.Metrics) []string {
	seen := map[string]bool{}
	ms := md.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics()
	for i := 0; i < ms.Len(); i++ {
		m := ms.At(i)
		kind := m.Type().String()
		var dps pmetric.NumberDataPointSlice
		if m.Type() == pmetric.MetricTypeSum {
			dps = m.Sum().DataPoints()
		} else {
			dps = m.Gauge().DataPoints()
		}
		for j := 0; j < dps.Len(); j++ {
			var keys []string
			for k := range dps.At(j).Attributes().AsRaw() {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			seen[fmt.Sprintf("%s|%s|%s|%s", m.Name(), m.Unit(), kind, strings.Join(keys, ","))] = true
		}
	}
	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func TestScrape_TheSeriesMatchTheLinuxSmartReceiverForTheSameFixtures(t *testing.T) {
	golden, err := os.ReadFile("../smartreceiver/testdata/series-signature.golden")
	require.NoError(t, err)

	b := newFakeBus()
	ata := sataDisk(smarttest.HealthyATA("SN1"))
	ata.tempErr, ata.predErr = smartdecode.ErrUnsupported, smartdecode.ErrUnsupported
	nvme := nvmeDisk(smarttest.HealthyNVMe("NV1"))
	nvme.tempErr, nvme.predErr = smartdecode.ErrUnsupported, smartdecode.ErrUnsupported
	b.add("PhysicalDrive0", ata)
	b.add("PhysicalDrive1", nvme)
	got := strings.Join(seriesSignature(newHarness(t, b).scrape()), "\n") + "\n"

	assert.Equal(t, string(golden), got, "names, units, kinds and attribute keys are the ones of the smart receiver")
}

func TestBuilderPoint_OddAttributeList_Panics(t *testing.T) {
	b := newBuilder(pmetric.NewMetrics(), 0, 0)
	odd := []string{hwschema.AttrSensorLocation}
	assert.Panics(t, func() { b.point(hwschema.MetricTemperature, ident{}, odd...) })
}
