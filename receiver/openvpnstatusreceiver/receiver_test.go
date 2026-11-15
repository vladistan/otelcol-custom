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

func newHarness(t *testing.T, join bool) *harness {
	t.Helper()
	core, logs := observer.New(zapcore.DebugLevel)
	settings := receivertest.NewNopSettings(NewFactory().Type())
	settings.Logger = zap.New(core)
	cfg := &Config{Path: filepath.Join(t.TempDir(), "status.log"), PollInterval: time.Second, JoinRoutingTable: join}
	sink := new(consumertest.LogsSink)
	return &harness{recv: newStatusReceiver(settings, cfg, sink), sink: sink, logs: logs, path: cfg.Path}
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
	assert.Equal(t, "alice", attrStr(t, recs[0], "common_name"))
	assert.Equal(t, "203.0.113.5:51234", attrStr(t, recs[0], "real_address"))
	assert.Equal(t, "2026-11-13 09:00:00", attrStr(t, recs[0], "connected_since"))
	received, _ := recs[0].Attributes().Get("bytes_received")
	sent, _ := recs[0].Attributes().Get("bytes_sent")
	assert.Equal(t, pcommon.ValueTypeInt, received.Type())
	assert.Equal(t, int64(1000), received.Int())
	assert.Equal(t, pcommon.ValueTypeInt, sent.Type())
	assert.Equal(t, int64(2000), sent.Int())
	assert.Equal(t, "bob", attrStr(t, recs[1], "common_name"))
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
			assert.Equal(t, "bob", attrStr(t, recs[1], "common_name"))
			sent, _ := recs[1].Attributes().Get("bytes_sent")
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
	received, _ := recs[2].Attributes().Get("bytes_received")
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
	assert.Equal(t, "alice", attrStr(t, recs[0], "common_name"))
	assert.Equal(t, "carol", attrStr(t, recs[1], "common_name"))
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
	assert.Equal(t, "UNDEF", attrStr(t, h.records()[0], "common_name"))
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

	restarted := newStatusReceiver(receivertest.NewNopSettings(NewFactory().Type()), h.recv.config, h.sink)
	restarted.poll(context.Background())
	restarted.poll(context.Background())

	assert.Len(t, h.records(), 4)
}

func TestPollConsumerErrorKeepsTheSnapshotForTheNextPoll(t *testing.T) {
	h := newHarness(t, false)
	h.writeFixture(t, "v1-two-clients.txt")
	h.recv.consumer = consumertest.NewErr(errors.New("downstream refused"))

	h.poll()
	assert.Equal(t, 1, h.logs.FilterLevelExact(zapcore.ErrorLevel).Len())

	h.recv.consumer = h.sink
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
