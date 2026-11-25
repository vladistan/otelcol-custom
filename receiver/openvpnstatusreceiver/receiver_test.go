package openvpnstatusreceiver

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component/componenttest"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/receiver/receivertest"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

type harness struct {
	recv *statusReceiver
	sink *consumertest.LogsSink
	logs *observer.ObservedLogs
	path string
}

type metricsHarness struct {
	recv *statusReceiver
	sink *consumertest.MetricsSink
	logs *observer.ObservedLogs
	path string
}

func newHarness(t *testing.T, join bool) *harness {
	t.Helper()
	core, logs := observer.New(zapcore.DebugLevel)
	settings := receivertest.NewNopSettings(NewFactory().Type())
	settings.Logger = zap.New(core)
	cfg := &Config{Path: filepath.Join(t.TempDir(), "status.log"), PollInterval: time.Second, JoinRoutingTable: join, Output: outputLogs}
	sink := new(consumertest.LogsSink)
	return &harness{recv: newStatusReceiver(settings, cfg, sink, nil), sink: sink, logs: logs, path: cfg.Path}
}

func newMetricsHarness(t *testing.T) *metricsHarness {
	t.Helper()
	core, logs := observer.New(zapcore.DebugLevel)
	settings := receivertest.NewNopSettings(NewFactory().Type())
	settings.Logger = zap.New(core)
	cfg := &Config{Path: filepath.Join(t.TempDir(), "status.log"), PollInterval: time.Second, Output: outputMetrics}
	sink := new(consumertest.MetricsSink)
	return &metricsHarness{recv: newStatusReceiver(settings, cfg, nil, sink), sink: sink, logs: logs, path: cfg.Path}
}

func (h *metricsHarness) writeFixture(t *testing.T, name string) {
	t.Helper()
	require.NoError(t, os.WriteFile(h.path, readFixture(t, name), 0o600))
}

func (h *metricsHarness) poll() { h.recv.poll(context.Background()) }

// metric returns the one metric of that name from the last batch.
func (h *metricsHarness) metric(t *testing.T, name string) pmetric.Metric {
	t.Helper()
	all := h.sink.AllMetrics()
	require.NotEmpty(t, all)
	ms := all[len(all)-1].ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics()
	for i := 0; i < ms.Len(); i++ {
		if ms.At(i).Name() == name {
			return ms.At(i)
		}
	}
	require.FailNow(t, "metric not found: "+name)
	return pmetric.Metric{}
}

// sumPoints maps "user.name/client.address" to the data point of a sum.
func sumPoints(t *testing.T, m pmetric.Metric) map[string]pmetric.NumberDataPoint {
	t.Helper()
	require.Equal(t, pmetric.MetricTypeSum, m.Type())
	return pointsByClient(t, m.Sum().DataPoints())
}

func pointsByClient(t *testing.T, dps pmetric.NumberDataPointSlice) map[string]pmetric.NumberDataPoint {
	t.Helper()
	out := map[string]pmetric.NumberDataPoint{}
	for i := 0; i < dps.Len(); i++ {
		cn, ok := dps.At(i).Attributes().Get("user.name")
		require.True(t, ok)
		addr, ok := dps.At(i).Attributes().Get("client.address")
		require.True(t, ok)
		out[cn.Str()+"/"+addr.Str()] = dps.At(i)
	}
	return out
}

func (h *harness) writeFixture(t *testing.T, name string) {
	t.Helper()
	require.NoError(t, os.WriteFile(h.path, readFixture(t, name), 0o600))
}

func (h *harness) poll() { h.recv.poll(context.Background()) }

func (h *harness) records() []plog.LogRecord {
	var out []plog.LogRecord
	for _, batch := range h.sink.AllLogs() {
		for i := 0; i < batch.ResourceLogs().Len(); i++ {
			scopes := batch.ResourceLogs().At(i).ScopeLogs()
			for j := 0; j < scopes.Len(); j++ {
				recs := scopes.At(j).LogRecords()
				for k := 0; k < recs.Len(); k++ {
					out = append(out, recs.At(k))
				}
			}
		}
	}
	return out
}

func (h *harness) warnings() int { return h.logs.FilterLevelExact(zapcore.WarnLevel).Len() }

func attrStr(t *testing.T, rec plog.LogRecord, key string) string {
	t.Helper()
	v, ok := rec.Attributes().Get(key)
	require.True(t, ok, "attribute %q missing", key)
	return v.Str()
}

func TestPollV1TwoClientsEmitsTwoRecordsWithTypedAttributes(t *testing.T) {
	h := newHarness(t, false)
	h.writeFixture(t, "v1-two-clients.txt")

	h.poll()

	recs := h.records()
	require.Len(t, recs, 2)
	want := localTime(t, "2026-11-13 10:00:00")
	assert.True(t, want.Equal(recs[0].Timestamp().AsTime()))
	assert.Equal(t, "alice", attrStr(t, recs[0], "user.name"))
	assert.Equal(t, "203.0.113.5", attrStr(t, recs[0], "client.address"), "the port is dropped")
	assert.Equal(t, "2026-11-13 09:00:00", attrStr(t, recs[0], "vstan.openvpn.connected_since"))
	received, _ := recs[0].Attributes().Get("vstan.openvpn.bytes_received")
	sent, _ := recs[0].Attributes().Get("vstan.openvpn.bytes_sent")
	assert.Equal(t, pcommon.ValueTypeInt, received.Type())
	assert.Equal(t, int64(1000), received.Int())
	assert.Equal(t, pcommon.ValueTypeInt, sent.Type())
	assert.Equal(t, int64(2000), sent.Int())
	assert.Equal(t, "bob", attrStr(t, recs[1], "user.name"))
}

func TestPollV2AndV3EveryLayoutUsesTheTimeRowTimestamp(t *testing.T) {
	for _, name := range []string{"v2-csv.txt", "v2-tsv.txt", "v3-csv.txt", "v3-tsv.txt"} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, false)
			h.writeFixture(t, name)

			h.poll()

			recs := h.records()
			require.Len(t, recs, 2)
			assert.True(t, time.Unix(1794564000, 0).Equal(recs[1].Timestamp().AsTime()))
			assert.Equal(t, "bob", attrStr(t, recs[1], "user.name"))
			sent, _ := recs[1].Attributes().Get("vstan.openvpn.bytes_sent")
			assert.Equal(t, int64(4000), sent.Int())
		})
	}
}

func TestPollFileTruncatedAndRewrittenInPlaceEmitsTheNewSnapshot(t *testing.T) {
	h := newHarness(t, false)
	h.writeFixture(t, "v1-two-clients.txt")
	h.poll()

	h.writeFixture(t, "v1-two-clients-next.txt")
	h.poll()

	recs := h.records()
	require.Len(t, recs, 4)
	received, _ := recs[2].Attributes().Get("vstan.openvpn.bytes_received")
	assert.Equal(t, int64(1500), received.Int())
	assert.True(t, localTime(t, "2026-11-13 10:00:20").Equal(recs[2].Timestamp().AsTime()))
}

func TestPollUnchangedSnapshotEmitsOnce(t *testing.T) {
	h := newHarness(t, false)
	h.writeFixture(t, "v1-two-clients.txt")

	h.poll()
	h.poll()
	h.poll()

	assert.Len(t, h.records(), 2)
	assert.Len(t, h.sink.AllLogs(), 1, "one batch for three polls")
}

func TestPollZeroClientSnapshotEmitsNothingAndCountsAsEmitted(t *testing.T) {
	h := newHarness(t, false)
	h.writeFixture(t, "v1-zero-clients.txt")

	h.poll()

	assert.Empty(t, h.records())
	assert.Equal(t, "2026-11-13 10:01:00", h.recv.lastIdentity)
}

func TestPollSkippedInputEmitsNothingAndKeepsRunning(t *testing.T) {
	for _, name := range []string{"v1-header-only.txt", "v1-footer-only.txt", "v1-empty.txt", "v1-partial.txt", "unknown-shape.txt"} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, false)
			h.writeFixture(t, name)

			h.poll()

			assert.Empty(t, h.records())
			assert.Empty(t, h.recv.lastIdentity)
		})
	}
}

func TestPollPartialSnapshotIsReadAgainOnceItIsComplete(t *testing.T) {
	h := newHarness(t, false)
	h.writeFixture(t, "v1-partial.txt")
	h.poll()
	h.poll()

	h.writeFixture(t, "v1-two-clients.txt")
	h.poll()

	assert.Len(t, h.records(), 2)
	assert.Equal(t, 1, h.warnings(), "one WARN for two polls of the same partial file")
}

func TestPollJoinRoutingTableOnAddsVirtualAddressAndLastRef(t *testing.T) {
	h := newHarness(t, true)
	h.writeFixture(t, "v1-two-clients.txt")

	h.poll()

	recs := h.records()
	assert.Equal(t, "10.8.0.2", attrStr(t, recs[0], "virtual_address"))
	assert.Equal(t, "2026-11-13 09:59:50", attrStr(t, recs[0], "last_ref"))
	assert.Equal(t, "10.8.0.3", attrStr(t, recs[1], "virtual_address"))
}

func TestPollJoinRoutingTableOffLeavesBothUnset(t *testing.T) {
	h := newHarness(t, false)
	h.writeFixture(t, "v1-two-clients.txt")

	h.poll()

	for _, rec := range h.records() {
		_, hasVirtual := rec.Attributes().Get("virtual_address")
		_, hasRef := rec.Attributes().Get("last_ref")
		assert.False(t, hasVirtual)
		assert.False(t, hasRef)
	}
}

func TestPollJoinRoutingTableSharedNameTakesTheRowWithItsRealAddress(t *testing.T) {
	h := newHarness(t, true)
	h.writeFixture(t, "v1-duplicate-names.txt")

	h.poll()

	recs := h.records()
	require.Len(t, recs, 3)
	assert.Equal(t, "10.8.0.2", attrStr(t, recs[0], "virtual_address"))
	assert.Equal(t, "10.8.0.4", attrStr(t, recs[1], "virtual_address"))
	_, carolHasVirtual := recs[2].Attributes().Get("virtual_address")
	assert.False(t, carolHasVirtual, "no routing row for carol")
}

func TestPollV1OldUpdatedLayoutEmitsTimestampedRecords(t *testing.T) {
	h := newHarness(t, false)
	h.writeFixture(t, "v1-old-updated.txt")

	h.poll()

	recs := h.records()
	require.Len(t, recs, 2)
	want, err := time.ParseInLocation("2006-01-02 15:04:05", "2019-01-03 21:40:24", time.Local)
	require.NoError(t, err)
	assert.True(t, want.Equal(recs[0].Timestamp().AsTime()))
}

func TestPollJoinRoutingTableNewSessionWithoutRowGetsNothing(t *testing.T) {
	h := newHarness(t, true)
	h.writeFixture(t, "v1-second-session.txt")

	h.poll()

	recs := h.records()
	require.Len(t, recs, 2)
	assert.Equal(t, "10.8.0.2", attrStr(t, recs[0], "virtual_address"))
	assert.Equal(t, "2026-11-13 10:05:50", attrStr(t, recs[0], "last_ref"))
	_, hasVirtual := recs[1].Attributes().Get("virtual_address")
	_, hasRef := recs[1].Attributes().Get("last_ref")
	assert.False(t, hasVirtual)
	assert.False(t, hasRef)
}

func TestPollBadRowIsSkippedWithOneWarnAndOtherClientsEmit(t *testing.T) {
	for _, name := range []string{"v1-six-field-row.txt", "v2-new-section.txt"} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, false)
			h.writeFixture(t, name)

			h.poll()
			h.poll()
			h.poll()

			assert.Equal(t, 1, h.warnings(), "one WARN for three polls of the same file")
			assert.Len(t, h.sink.AllLogs(), 1)
		})
	}
}

func TestPollBadRowKeepsOtherClients(t *testing.T) {
	h := newHarness(t, false)
	h.writeFixture(t, "v1-six-field-row.txt")

	h.poll()

	recs := h.records()
	require.Len(t, recs, 2)
	assert.Equal(t, "alice", attrStr(t, recs[0], "user.name"))
	assert.Equal(t, "carol", attrStr(t, recs[1], "user.name"))
}

func TestPollMissingEndStillSkipsTheWholeSnapshot(t *testing.T) {
	h := newHarness(t, false)
	h.writeFixture(t, "v1-partial.txt")

	h.poll()

	assert.Empty(t, h.records())
	assert.Equal(t, 1, h.warnings())
}

func (h *harness) writePadded(t *testing.T, name string, size int) {
	t.Helper()
	data := readFixture(t, name)
	require.LessOrEqual(t, len(data), size)
	data = append(data, bytes.Repeat([]byte("\n"), size-len(data))...)
	require.NoError(t, os.WriteFile(h.path, data, 0o600))
}

func TestPollFileOverTheSizeCapIsNotLoaded(t *testing.T) {
	h := newHarness(t, false)
	h.writePadded(t, "v1-two-clients.txt", 1<<20+1)

	h.poll()
	h.poll()

	assert.Empty(t, h.records())
	assert.Equal(t, 1, h.warnings(), "one WARN for two polls of the same oversized file")
}

func TestPollFileExactlyAtTheSizeCapIsLoaded(t *testing.T) {
	h := newHarness(t, false)
	h.writePadded(t, "v1-two-clients.txt", 1<<20)

	h.poll()

	assert.Len(t, h.records(), 2)
	assert.Zero(t, h.warnings())
}

func TestPollUndefClientIsEmittedWithTheUndefName(t *testing.T) {
	h := newHarness(t, true)
	h.writeFixture(t, "v1-undef.txt")

	h.poll()

	require.Len(t, h.records(), 1)
	assert.Equal(t, "UNDEF", attrStr(t, h.records()[0], "user.name"))
}

func TestPollMissingFileWarnsOncePerStateChange(t *testing.T) {
	h := newHarness(t, false)

	h.poll()
	h.poll()
	assert.Equal(t, 1, h.warnings())

	h.writeFixture(t, "v1-two-clients.txt")
	h.poll()
	require.NoError(t, os.Remove(h.path))
	h.poll()

	assert.Equal(t, 2, h.warnings())
	assert.Len(t, h.records(), 2)
}

func TestPollUnreadablePathWarnsAndRetries(t *testing.T) {
	h := newHarness(t, false)
	h.recv.config.Path = t.TempDir()

	h.poll()
	h.poll()

	assert.Equal(t, 1, h.warnings())
	assert.Empty(t, h.records())
}

func TestPollAfterRestartEmitsTheCurrentSnapshotOnce(t *testing.T) {
	h := newHarness(t, false)
	h.writeFixture(t, "v1-two-clients.txt")
	h.poll()

	restarted := newStatusReceiver(receivertest.NewNopSettings(NewFactory().Type()), h.recv.config, h.sink, nil)
	restarted.poll(context.Background())
	restarted.poll(context.Background())

	assert.Len(t, h.records(), 4)
}

func TestPollConsumerErrorKeepsTheSnapshotForTheNextPoll(t *testing.T) {
	h := newHarness(t, false)
	h.writeFixture(t, "v1-two-clients.txt")
	h.recv.logs = consumertest.NewErr(errors.New("downstream refused"))

	h.poll()
	assert.Equal(t, 1, h.logs.FilterLevelExact(zapcore.ErrorLevel).Len())

	h.recv.logs = h.sink
	h.poll()

	assert.Len(t, h.records(), 2)
}

func TestStartPollsUntilShutdown(t *testing.T) {
	h := newHarness(t, false)
	h.recv.config.PollInterval = 10 * time.Millisecond
	h.writeFixture(t, "v1-two-clients.txt")

	require.NoError(t, h.recv.Start(context.Background(), componenttest.NewNopHost()))
	require.Eventually(t, func() bool { return h.sink.LogRecordCount() == 2 }, 2*time.Second, 5*time.Millisecond)
	require.NoError(t, h.recv.Shutdown(context.Background()))

	h.writeFixture(t, "v1-two-clients-next.txt")
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, 2, h.sink.LogRecordCount(), "no poll after Shutdown")
}

func TestShutdownBeforeStartReturnsNil(t *testing.T) {
	h := newHarness(t, false)

	assert.NoError(t, h.recv.Shutdown(context.Background()))
}

func TestPollLogsCarryTheIPOnlyForIPv6AndPrefixedAddresses(t *testing.T) {
	h := newHarness(t, false)
	h.writeFixture(t, "v1-ipv6.txt")

	h.poll()

	recs := h.records()
	require.Len(t, recs, 3)
	assert.Equal(t, "2001:db8::1", attrStr(t, recs[0], "client.address"))
	assert.Equal(t, "2001:db8::2", attrStr(t, recs[1], "client.address"))
	assert.Equal(t, "192.0.2.9", attrStr(t, recs[2], "client.address"))
}

func TestPollLogsJoinStillUsesTheFullAddressAndOutputsOnlyTheIP(t *testing.T) {
	h := newHarness(t, true)
	h.writeFixture(t, "v1-two-clients.txt")

	h.poll()

	recs := h.records()
	assert.Equal(t, "10.8.0.2", attrStr(t, recs[0], "virtual_address"))
	assert.Equal(t, "203.0.113.5", attrStr(t, recs[0], "client.address"))
}

func TestPollMetricsBytesAreMonotonicCumulativeSumsInBytes(t *testing.T) {
	h := newMetricsHarness(t)
	h.writeFixture(t, "v1-two-clients.txt")

	h.poll()

	for name, want := range map[string]map[string]int64{
		"openvpn.client.bytes_received": {"alice/203.0.113.5": 1000, "bob/198.51.100.7": 3000},
		"openvpn.client.bytes_sent":     {"alice/203.0.113.5": 2000, "bob/198.51.100.7": 4000},
	} {
		m := h.metric(t, name)
		assert.Equal(t, "By", m.Unit(), name)
		assert.True(t, m.Sum().IsMonotonic(), name)
		assert.Equal(t, pmetric.AggregationTemporalityCumulative, m.Sum().AggregationTemporality(), name)
		got := map[string]int64{}
		for k, dp := range sumPoints(t, m) {
			got[k] = dp.IntValue()
		}
		assert.Equal(t, want, got, name)
	}
}

func TestPollMetricsPointsStartAtTheSessionStartAndEndAtTheSnapshot(t *testing.T) {
	h := newMetricsHarness(t)
	h.writeFixture(t, "v1-two-clients.txt")

	h.poll()

	points := sumPoints(t, h.metric(t, "openvpn.client.bytes_received"))
	alice := points["alice/203.0.113.5"]
	assert.True(t, localTime(t, "2026-11-13 09:00:00").Equal(alice.StartTimestamp().AsTime()))
	assert.True(t, localTime(t, "2026-11-13 10:00:00").Equal(alice.Timestamp().AsTime()))
}

func TestPollMetricsConnectedSinceIsAGaugeOfUnixSeconds(t *testing.T) {
	h := newMetricsHarness(t)
	h.writeFixture(t, "v2-csv.txt")

	h.poll()

	m := h.metric(t, "openvpn.client.connected_since")
	assert.Equal(t, "s", m.Unit())
	require.Equal(t, pmetric.MetricTypeGauge, m.Type())
	points := pointsByClient(t, m.Gauge().DataPoints())
	assert.Equal(t, int64(1794560400), points["alice/203.0.113.5"].IntValue())
	assert.Equal(t, int64(1794562200), points["bob/198.51.100.7"].IntValue())
}

func TestPollMetricsCarryOnlyCommonNameAndIPAsAttributes(t *testing.T) {
	h := newMetricsHarness(t)
	h.writeFixture(t, "v1-ipv6.txt")

	h.poll()

	for _, name := range []string{"openvpn.client.bytes_received", "openvpn.client.bytes_sent"} {
		points := sumPoints(t, h.metric(t, name))
		assert.Contains(t, points, "carol/2001:db8::1")
		assert.Contains(t, points, "dave/2001:db8::2")
		assert.Contains(t, points, "erin/192.0.2.9")
		for _, dp := range points {
			assert.Equal(t, 2, dp.Attributes().Len(), "no port, no connected_since attribute")
		}
	}
}

func TestPollMetricsSessionsOfOneNameFromOneIPAreMergedIntoOneSeries(t *testing.T) {
	h := newMetricsHarness(t)
	h.writeFixture(t, "v1-same-ip.txt")

	h.poll()

	received := sumPoints(t, h.metric(t, "openvpn.client.bytes_received"))
	require.Len(t, received, 2)
	alice := received["alice/203.0.113.5"]
	assert.Equal(t, int64(1300), alice.IntValue())
	assert.True(t, localTime(t, "2026-11-13 09:00:00").Equal(alice.StartTimestamp().AsTime()), "the earliest session start")
	since := pointsByClient(t, h.metric(t, "openvpn.client.connected_since").Gauge().DataPoints())
	assert.Equal(t, localTime(t, "2026-11-13 09:00:00").Unix(), since["alice/203.0.113.5"].IntValue())
}

func TestPollMetricsClientWithoutAReadableStartHasNoStartAndNoGaugePoint(t *testing.T) {
	h := newMetricsHarness(t)
	h.writeFixture(t, "v1-bad-since.txt")

	h.poll()

	alice := sumPoints(t, h.metric(t, "openvpn.client.bytes_received"))["alice/203.0.113.5"]
	assert.Equal(t, int64(1000), alice.IntValue())
	assert.Zero(t, alice.StartTimestamp())
	assert.Zero(t, h.metric(t, "openvpn.client.connected_since").Gauge().DataPoints().Len())
}

func TestPollMetricsUnchangedSnapshotEmitsOnce(t *testing.T) {
	h := newMetricsHarness(t)
	h.writeFixture(t, "v1-two-clients.txt")

	h.poll()
	h.poll()

	assert.Len(t, h.sink.AllMetrics(), 1)
}

func TestPollMetricsNewSnapshotEmitsAgainWithTheNewCounters(t *testing.T) {
	h := newMetricsHarness(t)
	h.writeFixture(t, "v1-two-clients.txt")
	h.poll()

	h.writeFixture(t, "v1-two-clients-next.txt")
	h.poll()

	require.Len(t, h.sink.AllMetrics(), 2)
	assert.Equal(t, int64(1500), sumPoints(t, h.metric(t, "openvpn.client.bytes_received"))["alice/203.0.113.5"].IntValue())
}

func TestPollMetricsZeroClientSnapshotEmitsNothing(t *testing.T) {
	h := newMetricsHarness(t)
	h.writeFixture(t, "v1-zero-clients.txt")

	h.poll()

	assert.Empty(t, h.sink.AllMetrics())
	assert.Equal(t, "2026-11-13 10:01:00", h.recv.lastIdentity)
}

func TestPollMetricsClientWithAnUnreadableAddressIsSkippedWithOneWarn(t *testing.T) {
	h := newMetricsHarness(t)
	h.writeFixture(t, "v1-bad-address.txt")

	h.poll()
	h.poll()

	assert.Len(t, sumPoints(t, h.metric(t, "openvpn.client.bytes_received")), 1)
	assert.Equal(t, 1, h.logs.FilterLevelExact(zapcore.WarnLevel).Len())
}

func TestPollMetricsConsumerErrorKeepsTheSnapshotForTheNextPoll(t *testing.T) {
	h := newMetricsHarness(t)
	h.writeFixture(t, "v1-two-clients.txt")
	h.recv.metrics = consumertest.NewErr(errors.New("downstream refused"))

	h.poll()
	assert.Equal(t, 1, h.logs.FilterLevelExact(zapcore.ErrorLevel).Len())

	h.recv.metrics = h.sink
	h.poll()

	assert.Len(t, h.sink.AllMetrics(), 1)
}

func TestPollMetricsReceiverEmitsNoLogs(t *testing.T) {
	h := newMetricsHarness(t)
	h.writeFixture(t, "v1-two-clients.txt")

	h.poll()

	assert.Nil(t, h.recv.logs)
	assert.Equal(t, 6, h.sink.DataPointCount(), "two clients, three metrics")
}

func TestPollMetricsReconnectKeepsTheSeriesWithANewStartAndLowerBytes(t *testing.T) {
	h := newMetricsHarness(t)
	h.writeFixture(t, "v1-two-clients.txt")
	h.poll()

	h.writeFixture(t, "v1-reconnect.txt")
	h.poll()

	alice := sumPoints(t, h.metric(t, "openvpn.client.bytes_received"))["alice/203.0.113.5"]
	assert.Equal(t, int64(10), alice.IntValue(), "the new session counts from zero")
	assert.True(t, localTime(t, "2026-11-13 10:00:05").Equal(alice.StartTimestamp().AsTime()), "the start moved: a reset")
}

func TestPollMetricsMergedSumFallsWhenOneSessionEndsAndTheStartStays(t *testing.T) {
	h := newMetricsHarness(t)
	h.writeFixture(t, "v1-same-ip.txt")
	h.poll()
	before := sumPoints(t, h.metric(t, "openvpn.client.bytes_received"))["alice/203.0.113.5"]

	h.writeFixture(t, "v1-same-ip-one.txt")
	h.poll()

	after := sumPoints(t, h.metric(t, "openvpn.client.bytes_received"))["alice/203.0.113.5"]
	assert.Equal(t, int64(1300), before.IntValue())
	assert.Equal(t, int64(1100), after.IntValue(), "the sum fell")
	assert.Equal(t, before.StartTimestamp(), after.StartTimestamp(), "no reset marker")
}

func TestPollMetricsV3TabSeparatedFileUsesTheEpochColumn(t *testing.T) {
	h := newMetricsHarness(t)
	h.writeFixture(t, "v3-tsv.txt")

	h.poll()

	alice := sumPoints(t, h.metric(t, "openvpn.client.bytes_sent"))["alice/203.0.113.5"]
	assert.Equal(t, int64(2000), alice.IntValue())
	assert.True(t, time.Unix(1794560400, 0).Equal(alice.StartTimestamp().AsTime()))
	assert.True(t, time.Unix(1794564000, 0).Equal(alice.Timestamp().AsTime()))
}

func TestPollMetricsMappedIPv4AddressesShareTheSeriesWithThePlainForm(t *testing.T) {
	h := newMetricsHarness(t)
	h.writeFixture(t, "v1-mapped.txt")

	h.poll()

	points := sumPoints(t, h.metric(t, "openvpn.client.bytes_received"))
	require.Len(t, points, 1)
	assert.Equal(t, int64(30), points["alice/192.0.2.1"].IntValue(), "both forms of one IP merge")
}

func TestPollSkippedAddressWarningCarriesNoAddress(t *testing.T) {
	h := newMetricsHarness(t)
	h.writeFixture(t, "v1-bad-address.txt")

	h.poll()

	warns := h.logs.FilterLevelExact(zapcore.WarnLevel).All()
	require.Len(t, warns, 1)
	assert.NotContains(t, warns[0].ContextMap()["error"], "not-an-address")
}
