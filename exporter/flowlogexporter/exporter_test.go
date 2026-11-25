// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package flowlogexporter

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component/componenttest"
	"go.opentelemetry.io/collector/config/configoptional"
	"go.opentelemetry.io/collector/consumer/consumererror"
	"go.opentelemetry.io/collector/exporter"
	"go.opentelemetry.io/collector/exporter/exporterhelper"
	"go.uber.org/zap"
)

func columnsOf(query string) []string {
	_, list, _ := strings.Cut(query, "(")
	var cols []string
	for _, c := range strings.Split(strings.TrimSuffix(list, ")"), ",") {
		cols = append(cols, strings.Trim(strings.TrimSpace(c), "`"))
	}
	return cols
}

func TestInsert_OneBatchAnAppendPerRowAndOneSend(t *testing.T) {
	fake := &fakeConn{}
	r := started(t, testConfig(), fake)

	require.NoError(t, r.e.pushLogs(context.Background(), logsOf(flowAttrs(nil), flowAttrs(nil), flowAttrs(nil))))

	batches := fake.batchList()
	require.Len(t, batches, 1)
	assert.Len(t, batches[0].rows, 3)
	assert.Equal(t, 1, fake.sendCount())
	assert.True(t, batches[0].closed)
	assert.Equal(t, map[string]int64{"": 3}, r.counter("otelcol_flowlog_rows_inserted"))
}

func TestInsert_TheStatementNamesOnlyTheFilledColumns(t *testing.T) {
	fake := &fakeConn{}
	r := started(t, testConfig(), fake)

	require.NoError(t, r.e.pushLogs(context.Background(), logsOf(flowAttrs(nil))))

	query := fake.batchList()[0].query
	assert.True(t, strings.HasPrefix(query, "INSERT INTO `flowlogs`.`flow_log` ("), query)
	cols := columnsOf(query)
	assert.NotContains(t, cols, "loaded_at")
	assert.NotContains(t, cols, "src_mac", "a column the record does not fill is left out")
	assert.NotContains(t, cols, "icmp_type")
	for _, c := range cols {
		assert.Contains(t, mappedColumns, c)
	}
}

func TestInsert_OptionalColumnsAreFilledWhenTheAttributeIsPresent(t *testing.T) {
	fake := &fakeConn{}
	r := started(t, testConfig(), fake)
	rec := flowAttrs(map[string]any{"flow.src_mac": "00:11:22:33:44:55", "flow.icmp_type": int64(8)})

	require.NoError(t, r.e.pushLogs(context.Background(), logsOf(rec)))

	cols := columnsOf(fake.batchList()[0].query)
	assert.Contains(t, cols, "src_mac")
	assert.Contains(t, cols, "icmp_type")
}

func TestInsert_ARowWithoutAColumnAnotherRowFills_GetsTheZeroValue(t *testing.T) {
	fake := &fakeConn{}
	r := started(t, testConfig(), fake)
	with := flowAttrs(map[string]any{"flow.next_hop": "198.51.100.1", "flow.sequence_num": int64(9)})

	require.NoError(t, r.e.pushLogs(context.Background(), logsOf(with, flowAttrs(nil))))

	b := fake.batchList()[0]
	cols := columnsOf(b.query)
	next, seq := indexOf(cols, "next_hop"), indexOf(cols, "sequence_num")
	require.GreaterOrEqual(t, next, 0)
	assert.Equal(t, "198.51.100.1", b.rows[0][next])
	assert.Equal(t, "", b.rows[1][next])
	assert.Equal(t, uint32(9), b.rows[0][seq])
	assert.Equal(t, uint32(0), b.rows[1][seq])
	for _, row := range b.rows {
		assert.Len(t, row, len(cols))
	}
}

func indexOf(list []string, s string) int {
	for i, v := range list {
		if v == s {
			return i
		}
	}
	return -1
}

func TestInsert_RecordWithoutAnAddress_IsSkippedAndCounted(t *testing.T) {
	fake := &fakeConn{}
	r := started(t, testConfig(), fake)

	require.NoError(t, r.e.pushLogs(context.Background(), logsOf(flowAttrs(nil), flowAttrs(map[string]any{"source.address": nil}))))

	assert.Len(t, fake.batchList()[0].rows, 1)
	assert.Equal(t, map[string]int64{dropInvalid: 1}, r.counter("otelcol_flowlog_rows_dropped"))
}

func TestInsert_ABatchOfOnlyInvalidRecords_SendsNothing(t *testing.T) {
	fake := &fakeConn{}
	r := started(t, testConfig(), fake)

	require.NoError(t, r.e.pushLogs(context.Background(), logsOf(flowAttrs(map[string]any{"destination.address": nil}))))

	assert.Empty(t, fake.batchList())
}

func TestRetry_AFailingInsertIsRetriedThenDroppedAndCounted(t *testing.T) {
	fake := &fakeConn{sendErr: errBoom}
	r := started(t, testConfig(), fake)

	err := r.e.pushLogs(context.Background(), logsOf(flowAttrs(nil), flowAttrs(nil)))

	require.Error(t, err)
	assert.True(t, consumererror.IsPermanent(err), "the queue must not retry it again")
	assert.Greater(t, fake.sendCount(), 1, "it was retried")
	assert.Equal(t, map[string]int64{dropInsertFailed: 2}, r.counter("otelcol_flowlog_rows_dropped"))
	assert.Empty(t, r.counter("otelcol_flowlog_rows_inserted"))
}

func TestRetry_WithRetryOff_OneAttemptThenDropped(t *testing.T) {
	cfg := testConfig()
	cfg.BackOffConfig.Enabled = false
	fake := &fakeConn{sendErr: errBoom}
	r := started(t, cfg, fake)

	require.Error(t, r.e.pushLogs(context.Background(), logsOf(flowAttrs(nil))))

	assert.Equal(t, 1, fake.sendCount())
}

func TestRetry_AFailureThatPasses_IsInsertedOnTheLaterAttempt(t *testing.T) {
	fake := &fakeConn{sendErr: errBoom}
	r := started(t, testConfig(), fake)
	go func() {
		time.Sleep(5 * time.Millisecond)
		fake.mu.Lock()
		fake.sendErr = nil
		fake.mu.Unlock()
	}()

	require.NoError(t, r.e.pushLogs(context.Background(), logsOf(flowAttrs(nil))))

	assert.Equal(t, map[string]int64{"": 1}, r.counter("otelcol_flowlog_rows_inserted"))
}

// twoConns dials stale first and fresh after that, and counts the dials.
func twoConns(stale, fresh *fakeConn) (func(context.Context) (conn, error), *atomic.Int32) {
	var dials atomic.Int32
	return func(context.Context) (conn, error) {
		if dials.Add(1) == 1 {
			return stale, nil
		}
		return fresh, nil
	}, &dials
}

func startedDial(t *testing.T, dial func(context.Context) (conn, error), first *fakeConn) *testRig {
	t.Helper()
	r := newRigDial(t, testConfig(), first, dial)
	require.NoError(t, r.e.start(context.Background(), componenttest.NewNopHost()))
	t.Cleanup(func() { _ = r.e.shutdown(context.Background()) })
	require.Eventually(t, func() bool { return r.e.conn() != nil }, 2*time.Second, time.Millisecond)
	return r
}

func TestRetry_AConnectionErrorIsFollowedByAnInsertOnAFreshConnection(t *testing.T) {
	cases := map[string]*fakeConn{
		"prepare deadline": {prepareErr: context.DeadlineExceeded},
		"prepare eof":      {prepareErr: io.EOF},
		"send eof":         {sendErr: io.EOF},
	}
	for name, stale := range cases {
		t.Run(name, func(t *testing.T) {
			fresh := &fakeConn{}
			dial, dials := twoConns(stale, fresh)
			r := startedDial(t, dial, stale)

			require.NoError(t, r.e.pushLogs(context.Background(), logsOf(flowAttrs(nil))))

			assert.EqualValues(t, 2, dials.Load())
			assert.Equal(t, 1, fresh.sendCount())
			assert.Equal(t, map[string]int64{"": 1}, r.counter("otelcol_flowlog_rows_inserted"))
			assert.Empty(t, r.counter("otelcol_flowlog_rows_dropped"))
		})
	}
}

func TestRetry_AFreshConnectionThatCannotBeDialled_IsRetriedUntilTheTimeLimit(t *testing.T) {
	stale := &fakeConn{prepareErr: io.EOF}
	var dials atomic.Int32
	dial := func(context.Context) (conn, error) {
		if dials.Add(1) == 1 {
			return stale, nil
		}
		return nil, errBoom
	}
	r := startedDial(t, dial, stale)

	err := r.e.pushLogs(context.Background(), logsOf(flowAttrs(nil)))

	require.Error(t, err)
	assert.True(t, consumererror.IsPermanent(err))
	assert.Greater(t, dials.Load(), int32(2), "each attempt dials again")
	assert.Equal(t, map[string]int64{dropInsertFailed: 1}, r.counter("otelcol_flowlog_rows_dropped"))
}

func TestRetry_Code202IsRetriedOnTheSameConnectionAndInsertedLater(t *testing.T) {
	fake := &fakeConn{prepareErr: &clickhouse.Exception{Code: 202, Message: "too many simultaneous queries"}}
	dial, dials := twoConns(fake, &fakeConn{})
	r := startedDial(t, dial, fake)
	go func() {
		time.Sleep(5 * time.Millisecond)
		fake.mu.Lock()
		fake.prepareErr = nil
		fake.mu.Unlock()
	}()

	require.NoError(t, r.e.pushLogs(context.Background(), logsOf(flowAttrs(nil))))

	assert.EqualValues(t, 1, dials.Load(), "the server answered, so the connection is kept")
	assert.Equal(t, map[string]int64{"": 1}, r.counter("otelcol_flowlog_rows_inserted"))
	assert.Empty(t, r.counter("otelcol_flowlog_rows_dropped"))
}

func TestRetry_Code202ThatNeverPasses_DropsOnlyAtTheTimeLimit(t *testing.T) {
	fake := &fakeConn{prepareErr: &clickhouse.Exception{Code: 202}}
	r := started(t, testConfig(), fake)

	err := r.e.pushLogs(context.Background(), logsOf(flowAttrs(nil), flowAttrs(nil)))

	require.Error(t, err)
	assert.True(t, consumererror.IsPermanent(err))
	assert.Greater(t, fake.prepareCalls, 1, "it was retried")
	assert.Equal(t, map[string]int64{dropInsertFailed: 2}, r.counter("otelcol_flowlog_rows_dropped"))
}

func TestSchemaError_DropsAtOnceAndWarnsOncePerEpisode(t *testing.T) {
	fake := &fakeConn{prepareErr: &clickhouse.Exception{Code: 47, Message: "unknown identifier"}}
	r := started(t, testConfig(), fake)

	for range 2 {
		err := r.e.pushLogs(context.Background(), logsOf(flowAttrs(nil), flowAttrs(nil)))
		require.Error(t, err)
		assert.True(t, consumererror.IsPermanent(err))
	}

	assert.Equal(t, 2, fake.prepareCalls, "no retry")
	assert.Equal(t, map[string]int64{dropSchema: 4}, r.counter("otelcol_flowlog_rows_dropped"))
	assert.Equal(t, 1, countContaining(warnings(r.logs), "does not take the rows"))
}

func TestSchemaError_ANewEpisodeAfterASuccess_WarnsAgain(t *testing.T) {
	fake := &fakeConn{prepareErr: &clickhouse.Exception{Code: 16}}
	r := started(t, testConfig(), fake)
	_ = r.e.pushLogs(context.Background(), logsOf(flowAttrs(nil)))
	fake.mu.Lock()
	fake.prepareErr = nil
	fake.mu.Unlock()
	require.NoError(t, r.e.pushLogs(context.Background(), logsOf(flowAttrs(nil))))
	fake.mu.Lock()
	fake.prepareErr = &clickhouse.Exception{Code: 16}
	fake.mu.Unlock()

	_ = r.e.pushLogs(context.Background(), logsOf(flowAttrs(nil)))

	assert.Equal(t, 2, countContaining(warnings(r.logs), "does not take the rows"))
}

func TestSchemaError_AnAppendErrorIsATypeMismatch(t *testing.T) {
	fake := &fakeConn{appendErr: errors.New("converting string to Int32")}
	r := started(t, testConfig(), fake)

	err := r.e.pushLogs(context.Background(), logsOf(flowAttrs(nil)))

	require.Error(t, err)
	assert.Equal(t, 1, fake.prepareCalls)
	assert.Equal(t, map[string]int64{dropSchema: 1}, r.counter("otelcol_flowlog_rows_dropped"))
}

func TestIsSchemaError(t *testing.T) {
	for code, want := range map[int32]bool{16: true, 47: true, 53: true, 60: true, 70: true, 349: true, 202: false, 210: false, 516: false} {
		assert.Equal(t, want, isSchemaError(fmt.Errorf("wrapped: %w", &clickhouse.Exception{Code: code})), code)
	}
	assert.False(t, isSchemaError(errBoom))
	assert.True(t, isSchemaError(errAppend{errBoom}))
}

func TestDropWarning_HoldsTheRowCountAndTheErrorNotTheRows(t *testing.T) {
	fake := &fakeConn{sendErr: errBoom}
	r := started(t, testConfig(), fake)

	_ = r.e.pushLogs(context.Background(), logsOf(flowAttrs(nil)))

	for _, entry := range r.logs.FilterLevelExact(zap.WarnLevel).All() {
		for k, v := range entry.ContextMap() {
			assert.Contains(t, []string{"rows", "error"}, k)
			assert.NotContains(t, fmt.Sprint(v), "198.51.100.5")
		}
	}
	assert.Equal(t, 1, countContaining(warnings(r.logs), "dropping the rows"))
}

func TestUnknownExporter_CountsEveryRecordAndWarnsOncePerAddress(t *testing.T) {
	fake := &fakeConn{}
	r := started(t, testConfig(), fake)
	stranger := flowAttrs(map[string]any{"flow.sampler_address": "192.0.2.99"})

	require.NoError(t, r.e.pushLogs(context.Background(), logsOf(stranger)))
	require.NoError(t, r.e.pushLogs(context.Background(), logsOf(stranger, stranger)))

	assert.Equal(t, map[string]int64{"": 3}, r.counter("otelcol_flowlog_unknown_exporter"))
	assert.Equal(t, 1, countContaining(warnings(r.logs), "not in routers"))
	assert.Equal(t, 0, countContaining(warnings(r.logs), "192.0.2.10"), "a listed exporter raises no warning")
}

func TestStart_ARefusedConnection_ReturnsNoErrorWarnsOnceAndInsertsLater(t *testing.T) {
	fake := &fakeConn{}
	var reachable atomic.Bool
	dial := func(context.Context) (conn, error) {
		if !reachable.Load() {
			return nil, errors.New("dial tcp: connection refused")
		}
		return fake, nil
	}
	r := newRigDial(t, testConfig(), fake, dial)

	require.NoError(t, r.e.start(context.Background(), componenttest.NewNopHost()))
	t.Cleanup(func() { _ = r.e.shutdown(context.Background()) })
	require.Eventually(t, func() bool { return countContaining(warnings(r.logs), "cannot reach ClickHouse") == 1 }, 2*time.Second, time.Millisecond)
	assert.ErrorIs(t, r.e.pushLogs(context.Background(), logsOf(flowAttrs(nil))), errNotConnected)
	reachable.Store(true)
	require.Eventually(t, func() bool { return r.e.conn() != nil }, 3*time.Second, 5*time.Millisecond)

	require.NoError(t, r.e.pushLogs(context.Background(), logsOf(flowAttrs(nil))))

	assert.Equal(t, 1, countContaining(warnings(r.logs), "cannot reach ClickHouse"), "one warning, however many tries")
	assert.Equal(t, map[string]int64{"": 1}, r.counter("otelcol_flowlog_rows_inserted"))
}

func TestStart_AnEmptyPassword_StartsWarnsOnceAndCountsOnce(t *testing.T) {
	cfg := testConfig()
	cfg.Password = ""
	r := started(t, cfg, &fakeConn{})

	assert.Equal(t, 1, countContaining(warnings(r.logs), "password is empty"))
	assert.Equal(t, map[string]int64{warnEmptyPassword: 1}, r.counter("otelcol_flowlog_config_warnings"))
}

func TestStart_APassword_RaisesNoConfigWarning(t *testing.T) {
	r := started(t, testConfig(), &fakeConn{})

	assert.Empty(t, r.counter("otelcol_flowlog_config_warnings"))
}

func TestSecrets_ThePasswordIsInNoLogErrorOrConfigDump(t *testing.T) {
	cfg := testConfig()
	secret := string(cfg.Password)
	fake := &fakeConn{prepareErr: errors.New("login refused")}
	dial := func(context.Context) (conn, error) { return nil, errors.New("dial tcp: connection refused") }
	r := newRigDial(t, cfg, fake, dial)
	require.NoError(t, r.e.start(context.Background(), componenttest.NewNopHost()))
	t.Cleanup(func() { _ = r.e.shutdown(context.Background()) })
	require.Eventually(t, func() bool { return len(r.logs.All()) > 0 }, 2*time.Second, time.Millisecond)

	err := r.e.pushLogs(context.Background(), logsOf(flowAttrs(nil)))

	var out strings.Builder
	for _, e := range r.logs.All() {
		fmt.Fprintf(&out, "%s %v\n", e.Message, e.ContextMap())
	}
	fmt.Fprintf(&out, "%v\n%+v\n%#v\n%s\n", err, cfg, cfg, cfg.Password)
	assert.NotContains(t, out.String(), secret)
}

func TestQueue_AFullQueueDropsTheBatchAtOnceAndAnotherExporterStillExports(t *testing.T) {
	cfg := testConfig()
	cfg.QueueSettings = queueOf(1, 1)
	blocked := &fakeConn{block: make(chan struct{})}
	set, _, reader := rigSettings()
	slow, err := buildLogs(context.Background(), set, cfg, func(context.Context) (conn, error) { return blocked, nil })
	require.NoError(t, err)
	require.NoError(t, slow.Start(context.Background(), componenttest.NewNopHost()))
	t.Cleanup(func() { close(blocked.block); _ = slow.Shutdown(context.Background()) })

	fast := &fakeConn{}
	other, err := buildLogs(context.Background(), mustSettings(), testConfig(), func(context.Context) (conn, error) { return fast, nil })
	require.NoError(t, err)
	require.NoError(t, other.Start(context.Background(), componenttest.NewNopHost()))
	t.Cleanup(func() { _ = other.Shutdown(context.Background()) })

	// one batch blocks in its insert, one fills the queue; the rest drop
	for range 6 {
		require.NoError(t, slow.ConsumeLogs(context.Background(), logsOf(flowAttrs(nil))))
		time.Sleep(10 * time.Millisecond)
	}
	start := time.Now()
	require.NoError(t, other.ConsumeLogs(context.Background(), logsOf(flowAttrs(nil))))

	assert.Eventually(t, func() bool { return fast.sendCount() == 1 }, time.Second, time.Millisecond)
	assert.Less(t, time.Since(start), time.Second)
	got := (&testRig{t: t, reader: reader}).counter("otelcol_flowlog_rows_dropped")
	assert.Positive(t, got[dropQueueFull], "got %v", got)
}

func mustSettings() exporter.Settings {
	s, _, _ := rigSettings()
	return s
}

func queueOf(size int64, consumers int) configoptional.Optional[exporterhelper.QueueBatchConfig] {
	q := exporterhelper.NewDefaultQueueConfig()
	q.QueueSize = size
	q.NumConsumers = consumers
	return configoptional.Some(q)
}
