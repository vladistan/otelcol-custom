// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package flowlogexporter

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/component/componenttest"
	"go.opentelemetry.io/collector/exporter"
	"go.opentelemetry.io/collector/exporter/exportertest"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

// fakeBatch records what the exporter appends and sends.
type fakeBatch struct {
	conn      *fakeConn
	query     string
	rows      [][]any
	appendErr error
	closed    bool
}

func (b *fakeBatch) Append(v ...any) error {
	if b.appendErr != nil {
		return b.appendErr
	}
	b.rows = append(b.rows, append([]any(nil), v...))
	return nil
}

func (b *fakeBatch) Send() error {
	b.conn.mu.Lock()
	b.conn.sends++
	err := b.conn.sendErr
	b.conn.mu.Unlock()
	if b.conn.block != nil {
		<-b.conn.block
	}
	return err
}

func (b *fakeBatch) Close() error { b.closed = true; return nil }

// fakeConn is a ClickHouse connection that records its batches.
type fakeConn struct {
	mu           sync.Mutex
	batches      []*fakeBatch
	sends        int
	prepareCalls int
	prepareErr   error
	sendErr      error
	appendErr    error
	block        chan struct{}
}

func (f *fakeConn) PrepareBatch(_ context.Context, query string) (batch, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.prepareCalls++
	if f.prepareErr != nil {
		return nil, f.prepareErr
	}
	b := &fakeBatch{conn: f, query: query, appendErr: f.appendErr}
	f.batches = append(f.batches, b)
	return b, nil
}

func (f *fakeConn) Close() error { return nil }

func (f *fakeConn) sendCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sends
}

func (f *fakeConn) batchList() []*fakeBatch {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*fakeBatch(nil), f.batches...)
}

// testRig is an exporter with a fake connection, a log observer and a metric reader.
type testRig struct {
	t      *testing.T
	e      *logsExporter
	fake   *fakeConn
	logs   *observer.ObservedLogs
	reader *sdkmetric.ManualReader
}

func testConfig() *Config {
	cfg := createDefaultConfig().(*Config)
	cfg.Endpoint = "clickhouse.example.invalid:9000"
	cfg.Username = "writer"
	cfg.Password = "test-password-value" // pragma: allowlist secret
	cfg.Collector = "gateway-0"
	cfg.Routers = []Router{{Address: "192.0.2.10", AccountID: "site-a", Region: "site-a", SamplingRate: 100, Direction: "ingress"}}
	cfg.BackOffConfig.InitialInterval = time.Millisecond
	cfg.BackOffConfig.MaxInterval = 2 * time.Millisecond
	cfg.BackOffConfig.MaxElapsedTime = 20 * time.Millisecond
	return cfg
}

func newRig(t *testing.T, cfg *Config, fake *fakeConn) *testRig {
	t.Helper()
	return newRigDial(t, cfg, fake, func(context.Context) (conn, error) { return fake, nil })
}

func rigSettings() (set exporter.Settings, logs *observer.ObservedLogs, reader *sdkmetric.ManualReader) {
	core, logs := observer.New(zap.DebugLevel)
	reader = sdkmetric.NewManualReader()
	set = exportertest.NewNopSettings(component.MustNewType(typeStr))
	set.Logger = zap.New(core)
	set.MeterProvider = sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	return set, logs, reader
}

func newRigDial(t *testing.T, cfg *Config, fake *fakeConn, dial func(context.Context) (conn, error)) *testRig {
	t.Helper()
	require.NoError(t, cfg.Validate())
	set, logs, reader := rigSettings()
	e, err := newLogsExporter(cfg, set, cfg.Collector, dial)
	require.NoError(t, err)
	return &testRig{t: t, e: e, fake: fake, logs: logs, reader: reader}
}

// started returns a rig whose exporter has started and connected.
func started(t *testing.T, cfg *Config, fake *fakeConn) *testRig {
	t.Helper()
	r := newRig(t, cfg, fake)
	require.NoError(t, r.e.start(context.Background(), componenttest.NewNopHost()))
	t.Cleanup(func() { _ = r.e.shutdown(context.Background()) })
	require.Eventually(t, func() bool { return r.e.conn() != nil }, 2*time.Second, time.Millisecond)
	return r
}

// counter returns the sum of a counter by the value of the reason attribute.
func (r *testRig) counter(name string) map[string]int64 {
	r.t.Helper()
	var rm metricdata.ResourceMetrics
	require.NoError(r.t, r.reader.Collect(context.Background(), &rm))
	got := map[string]int64{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != name {
				continue
			}
			for _, dp := range m.Data.(metricdata.Sum[int64]).DataPoints {
				v, _ := dp.Attributes.Value(attribute.Key(reasonKey))
				got[v.AsString()] += dp.Value
			}
		}
	}
	return got
}

// flowAttrs is the attribute set of a TCP flow record from the receiver, with overrides; a nil value removes a key.
func flowAttrs(over map[string]any) map[string]any {
	a := map[string]any{
		"source.address": "198.51.100.5", "source.port": int64(51234),
		"destination.address": "203.0.113.9", "destination.port": int64(443),
		"network.transport": "tcp", "flow.io.bytes": int64(1500), "flow.io.packets": int64(3),
		"flow.type": "netflow_v9", "flow.sampler_address": "192.0.2.10",
		"flow.start": int64(1795129200123000000), "flow.end": int64(1795129201456000000),
		"flow.time_received": int64(1795129202000000000), "flow.sampling_rate": int64(0),
		"flow.in_if": int64(1), "flow.out_if": int64(2), "flow.tcp_flags": int64(24), "flow.ip_tos": int64(184),
		"flow.forwarding_status": int64(64),
	}
	for k, v := range over {
		if v == nil {
			delete(a, k)
			continue
		}
		a[k] = v
	}
	return a
}

// logsOf builds one batch of records with the given attribute sets.
func logsOf(sets ...map[string]any) plog.Logs {
	ld := plog.NewLogs()
	records := ld.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty().LogRecords()
	for _, s := range sets {
		r := records.AppendEmpty()
		if err := r.Attributes().FromRaw(s); err != nil {
			panic(err)
		}
	}
	return ld
}

// warnings returns the messages of the entries at WARN level.
func warnings(logs *observer.ObservedLogs) []string {
	var out []string
	for _, e := range logs.FilterLevelExact(zap.WarnLevel).All() {
		out = append(out, e.Message)
	}
	return out
}

func countContaining(msgs []string, part string) int {
	n := 0
	for _, m := range msgs {
		if strings.Contains(m, part) {
			n++
		}
	}
	return n
}

var errBoom = errors.New("boom")
