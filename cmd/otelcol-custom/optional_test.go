// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/component/componentstatus"
	"go.opentelemetry.io/collector/component/componenttest"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/otelcol"
	"go.opentelemetry.io/collector/receiver"
	"go.opentelemetry.io/collector/receiver/receivertest"
	otelmetric "go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// fakeReceiver records its calls. Start fails with startErr, on every call when okAtCall is 0 and on the
// calls before call number okAtCall otherwise.
type fakeReceiver struct {
	mu        sync.Mutex
	startErr  error
	okAtCall  int
	calls     int
	shutdowns int
}

func (f *fakeReceiver) Start(context.Context, component.Host) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.startErr != nil && (f.okAtCall == 0 || f.calls < f.okAtCall) {
		return f.startErr
	}
	return nil
}

func (f *fakeReceiver) Shutdown(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.shutdowns++
	return nil
}

func (f *fakeReceiver) startCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *fakeReceiver) shutdownCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.shutdowns
}

// statusHost is a host that records the component status events it is sent.
type statusHost struct {
	component.Host
	mu     sync.Mutex
	events []*componentstatus.Event
}

func newStatusHost() *statusHost { return &statusHost{Host: componenttest.NewNopHost()} }

func (h *statusHost) Report(ev *componentstatus.Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.events = append(h.events, ev)
}

func (h *statusHost) statuses() []componentstatus.Status {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []componentstatus.Status
	for _, ev := range h.events {
		out = append(out, ev.Status())
	}
	return out
}

func (h *statusHost) errors() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []string
	for _, ev := range h.events {
		if ev.Err() != nil {
			out = append(out, ev.Err().Error())
		}
	}
	return out
}

// fastRetries makes the retry delays milliseconds for the length of one test.
func fastRetries(t *testing.T) {
	t.Helper()
	oldInitial, oldMax := retryInitialDelay, retryMaxDelay
	retryInitialDelay, retryMaxDelay = time.Millisecond, 4*time.Millisecond
	t.Cleanup(func() { retryInitialDelay, retryMaxDelay = oldInitial, oldMax })
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	require.Eventually(t, cond, 5*time.Second, time.Millisecond, what)
}

type fakeConfig struct{ Marker string }

// newFakeFactory builds a receiver factory of the given type with metrics (beta) and logs (alpha) support.
func newFakeFactory(t *testing.T, name string, fake *fakeReceiver) receiver.Factory {
	t.Helper()
	typ := component.MustNewType(name)
	return receiver.NewFactory(
		typ,
		func() component.Config { return &fakeConfig{Marker: name} },
		receiver.WithMetrics(func(context.Context, receiver.Settings, component.Config, consumer.Metrics) (receiver.Metrics, error) {
			return fake, nil
		}, component.StabilityLevelBeta),
		receiver.WithLogs(func(context.Context, receiver.Settings, component.Config, consumer.Logs) (receiver.Logs, error) {
			return fake, nil
		}, component.StabilityLevelAlpha),
	)
}

type testEnv struct {
	logs   *observer.ObservedLogs
	reader *metric.ManualReader
	set    receiver.Settings
}

func newTestEnv(t *testing.T, typ string) *testEnv {
	t.Helper()
	core, logs := observer.New(zapcore.DebugLevel)
	reader := metric.NewManualReader()
	provider := metric.NewMeterProvider(metric.WithReader(reader))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	set := receivertest.NewNopSettings(component.MustNewType(typ))
	set.ID = component.NewID(component.MustNewType(typ))
	set.Logger = zap.New(core)
	set.MeterProvider = provider
	return &testEnv{logs: logs, reader: reader, set: set}
}

func (e *testEnv) skipped(t *testing.T) map[string]int64 {
	t.Helper()
	var rm metricdata.ResourceMetrics
	require.NoError(t, e.reader.Collect(context.Background(), &rm))
	out := map[string]int64{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != "otelcol_optional_receiver_skipped" {
				continue
			}
			for _, dp := range m.Data.(metricdata.Sum[int64]).DataPoints {
				name, _ := dp.Attributes.Value("receiver")
				out[name.AsString()] += dp.Value
			}
		}
	}
	return out
}

func (e *testEnv) warnings() []observer.LoggedEntry {
	return e.logs.FilterLevelExact(zapcore.WarnLevel).All()
}

func wrapped(t *testing.T, name string, fake *fakeReceiver, optional ...string) receiver.Factory {
	t.Helper()
	factories := otelcol.Factories{Receivers: map[component.Type]receiver.Factory{
		component.MustNewType(name): newFakeFactory(t, name, fake),
	}}
	got := wrapOptionalReceivers(factories, optional...)
	return got.Receivers[component.MustNewType(name)]
}

func TestOptionalReceiverTypesAreTheRulingList(t *testing.T) {
	assert.Equal(t, []string{"docker_stats", "tcp_check"}, optionalReceiverTypes,
		"a core receiver must not be added here: its failure stops the collector on purpose")
}

func TestWrapReplacesOnlyTheListedReceivers(t *testing.T) {
	optional := newFakeFactory(t, "docker_stats", &fakeReceiver{})
	core := newFakeFactory(t, "journald", &fakeReceiver{})
	factories := otelcol.Factories{Receivers: map[component.Type]receiver.Factory{
		optional.Type(): optional,
		core.Type():     core,
	}}

	got := wrapOptionalReceivers(factories, "docker_stats", "tcp_check")

	assert.NotSame(t, optional, got.Receivers[optional.Type()], "the listed receiver is wrapped")
	assert.Same(t, core, got.Receivers[core.Type()], "an unlisted receiver is left as it is")
	assert.Len(t, got.Receivers, 2, "a listed type the build does not have is not invented")
}

func TestWrappedFactoryKeepsTypeConfigSignalsAndStability(t *testing.T) {
	factory := wrapped(t, "docker_stats", &fakeReceiver{}, "docker_stats")

	assert.Equal(t, component.MustNewType("docker_stats"), factory.Type())
	assert.Equal(t, &fakeConfig{Marker: "docker_stats"}, factory.CreateDefaultConfig())
	assert.Equal(t, component.StabilityLevelBeta, factory.MetricsStability())
	assert.Equal(t, component.StabilityLevelAlpha, factory.LogsStability())
	assert.Equal(t, component.StabilityLevelUndefined, factory.TracesStability())

	env := newTestEnv(t, "docker_stats")
	_, err := factory.CreateTraces(context.Background(), env.set, factory.CreateDefaultConfig(), consumertest.NewNop())
	assert.Error(t, err, "a signal the receiver does not support is still refused")
}

func TestFailedStartIsLoggedCountedAndSkipped(t *testing.T) {
	fake := &fakeReceiver{startErr: errors.New("api version 1.44 is too new")}
	factory := wrapped(t, "docker_stats", fake, "docker_stats")
	env := newTestEnv(t, "docker_stats")

	rcv, err := factory.CreateMetrics(context.Background(), env.set, factory.CreateDefaultConfig(), consumertest.NewNop())
	require.NoError(t, err)

	require.NoError(t, rcv.Start(context.Background(), componenttest.NewNopHost()), "the collector keeps running")
	assert.Equal(t, 1, fake.startCalls())

	warnings := env.warnings()
	require.Len(t, warnings, 1)
	assert.Contains(t, warnings[0].Message, "skipped")
	fields := warnings[0].ContextMap()
	assert.Equal(t, "docker_stats", fields["receiver"])
	assert.Contains(t, fields["error"], "api version 1.44 is too new")
	assert.Equal(t, map[string]int64{"docker_stats": 1}, env.skipped(t))

	require.NoError(t, rcv.Shutdown(context.Background()))
	assert.Zero(t, fake.shutdownCalls(), "a receiver that never started is not shut down")
}

func TestSuccessfulStartPassesThrough(t *testing.T) {
	fake := &fakeReceiver{}
	factory := wrapped(t, "tcp_check", fake, "tcp_check")
	env := newTestEnv(t, "tcp_check")

	rcv, err := factory.CreateLogs(context.Background(), env.set, factory.CreateDefaultConfig(), consumertest.NewNop())
	require.NoError(t, err)
	require.NoError(t, rcv.Start(context.Background(), componenttest.NewNopHost()))
	require.NoError(t, rcv.Shutdown(context.Background()))

	assert.Equal(t, 1, fake.startCalls())
	assert.Equal(t, 1, fake.shutdownCalls())
	assert.Empty(t, env.warnings())
	assert.Zero(t, env.skipped(t)["tcp_check"], "the counter starts at zero and stays there")
}

func TestStartFailureOfAnUnlistedReceiverStaysFatal(t *testing.T) {
	fake := &fakeReceiver{startErr: errors.New("cannot open the journal")}
	core := newFakeFactory(t, "journald", fake)
	factories := otelcol.Factories{Receivers: map[component.Type]receiver.Factory{core.Type(): core}}
	got := wrapOptionalReceivers(factories, "docker_stats", "tcp_check")
	env := newTestEnv(t, "journald")

	rcv, err := got.Receivers[core.Type()].CreateMetrics(context.Background(), env.set, core.CreateDefaultConfig(), consumertest.NewNop())
	require.NoError(t, err)

	assert.Error(t, rcv.Start(context.Background(), componenttest.NewNopHost()))
	assert.Empty(t, env.skipped(t), "an unlisted receiver gets no counter")
}

func TestFailureToCreateIsNotHidden(t *testing.T) {
	typ := component.MustNewType("docker_stats")
	createErr := errors.New("bad endpoint")
	inner := receiver.NewFactory(typ, func() component.Config { return &fakeConfig{} },
		receiver.WithMetrics(func(context.Context, receiver.Settings, component.Config, consumer.Metrics) (receiver.Metrics, error) {
			return nil, createErr
		}, component.StabilityLevelBeta))
	got := wrapOptionalReceivers(otelcol.Factories{Receivers: map[component.Type]receiver.Factory{typ: inner}}, "docker_stats")
	env := newTestEnv(t, "docker_stats")

	_, err := got.Receivers[typ].CreateMetrics(context.Background(), env.set, inner.CreateDefaultConfig(), consumertest.NewNop())
	assert.ErrorIs(t, err, createErr, "a config that cannot build a receiver still stops the collector")
}

func TestFactoriesFuncWrapsWhatTheBuildReturns(t *testing.T) {
	inner := newFakeFactory(t, "docker_stats", &fakeReceiver{})
	build := func() (otelcol.Factories, error) {
		return otelcol.Factories{Receivers: map[component.Type]receiver.Factory{inner.Type(): inner}}, nil
	}

	got, err := withOptionalReceivers(build)()
	require.NoError(t, err)
	assert.NotSame(t, inner, got.Receivers[inner.Type()])

	failing := func() (otelcol.Factories, error) { return otelcol.Factories{}, errors.New("no components") }
	_, err = withOptionalReceivers(failing)()
	assert.EqualError(t, err, "no components")
}

func TestCounterExistsAtZeroBeforeAnySkip(t *testing.T) {
	factory := wrapped(t, "docker_stats", &fakeReceiver{}, "docker_stats")
	env := newTestEnv(t, "docker_stats")

	_, err := factory.CreateMetrics(context.Background(), env.set, factory.CreateDefaultConfig(), consumertest.NewNop())
	require.NoError(t, err)

	assert.Equal(t, map[string]int64{"docker_stats": 0}, env.skipped(t), "absent() and increase() rules need a baseline")
}

func TestSkippedReceiverIsRetriedAndRecovers(t *testing.T) {
	fastRetries(t)
	fake := &fakeReceiver{startErr: errors.New("docker daemon is not up"), okAtCall: 4}
	factory := wrapped(t, "docker_stats", fake, "docker_stats")
	env := newTestEnv(t, "docker_stats")
	host := newStatusHost()

	rcv, err := factory.CreateMetrics(context.Background(), env.set, factory.CreateDefaultConfig(), consumertest.NewNop())
	require.NoError(t, err)
	require.NoError(t, rcv.Start(context.Background(), host))

	waitFor(t, "the receiver starts on the fourth call", func() bool {
		return len(env.logs.FilterMessageSnippet("started after").All()) == 1
	})
	assert.Equal(t, 4, fake.startCalls())
	assert.Equal(t, map[string]int64{"docker_stats": 1}, env.skipped(t), "a skip is counted once, not once per retry")
	recovered := env.logs.FilterMessageSnippet("started after").All()[0]
	assert.Equal(t, zapcore.InfoLevel, recovered.Level)
	assert.Equal(t, "docker_stats", recovered.ContextMap()["receiver"])
	assert.Equal(t, componentstatus.StatusOK, host.statuses()[len(host.statuses())-1], "recovery clears the error status")

	require.NoError(t, rcv.Shutdown(context.Background()))
	assert.Equal(t, 1, fake.shutdownCalls(), "a receiver that started late is shut down")
	count := fake.startCalls()
	time.Sleep(20 * time.Millisecond)
	assert.Equal(t, count, fake.startCalls(), "no retry after the receiver started")
}

func TestRetryWarnsOnTheFirstFailureAndEveryTenth(t *testing.T) {
	fastRetries(t)
	fake := &fakeReceiver{startErr: errors.New("docker daemon is not up")}
	factory := wrapped(t, "docker_stats", fake, "docker_stats")
	env := newTestEnv(t, "docker_stats")

	rcv, err := factory.CreateMetrics(context.Background(), env.set, factory.CreateDefaultConfig(), consumertest.NewNop())
	require.NoError(t, err)
	require.NoError(t, rcv.Start(context.Background(), componenttest.NewNopHost()))
	waitFor(t, "at least 25 start calls", func() bool { return fake.startCalls() >= 25 })
	require.NoError(t, rcv.Shutdown(context.Background()))

	failures := fake.startCalls()
	assert.Len(t, env.warnings(), 1+failures/10, "a WARN on failure 1 and on every tenth failure after it")
	assert.Zero(t, fake.shutdownCalls())
	assert.Equal(t, map[string]int64{"docker_stats": 1}, env.skipped(t))
}

func TestShutdownStopsTheRetry(t *testing.T) {
	fastRetries(t)
	fake := &fakeReceiver{startErr: errors.New("docker daemon is not up")}
	factory := wrapped(t, "docker_stats", fake, "docker_stats")
	env := newTestEnv(t, "docker_stats")

	rcv, err := factory.CreateMetrics(context.Background(), env.set, factory.CreateDefaultConfig(), consumertest.NewNop())
	require.NoError(t, err)
	require.NoError(t, rcv.Start(context.Background(), componenttest.NewNopHost()))
	waitFor(t, "a first retry", func() bool { return fake.startCalls() >= 2 })

	require.NoError(t, rcv.Shutdown(context.Background()))
	count := fake.startCalls()
	time.Sleep(20 * time.Millisecond)
	assert.Equal(t, count, fake.startCalls(), "no start call after Shutdown")
	assert.Zero(t, fake.shutdownCalls())
}

func TestRetryDelayStartsAt30sDoublesAndCapsAt5m(t *testing.T) {
	assert.Equal(t, 30*time.Second, retryInitialDelay)
	assert.Equal(t, 5*time.Minute, retryMaxDelay)
	assert.Equal(t, 60*time.Second, nextRetryDelay(30*time.Second))
	assert.Equal(t, 5*time.Minute, nextRetryDelay(4*time.Minute))
	assert.Equal(t, 5*time.Minute, nextRetryDelay(5*time.Minute))
}

func TestFailedRetriesReportARecoverableErrorStatus(t *testing.T) {
	fastRetries(t)
	fake := &fakeReceiver{startErr: errors.New("docker daemon is not up")}
	factory := wrapped(t, "docker_stats", fake, "docker_stats")
	env := newTestEnv(t, "docker_stats")
	host := newStatusHost()

	rcv, err := factory.CreateMetrics(context.Background(), env.set, factory.CreateDefaultConfig(), consumertest.NewNop())
	require.NoError(t, err)
	require.NoError(t, rcv.Start(context.Background(), host))
	waitFor(t, "a status event", func() bool { return len(host.statuses()) > 0 })
	require.NoError(t, rcv.Shutdown(context.Background()))

	assert.Equal(t, componentstatus.StatusRecoverableError, host.statuses()[0])
	assert.Contains(t, host.errors()[0], "docker daemon is not up")
}

func TestCredentialsInAnErrorAreRedacted(t *testing.T) {
	fake := &fakeReceiver{startErr: errors.New(`cannot reach tcp://admin:s3cret@docker.example:2375: refused`)}
	factory := wrapped(t, "docker_stats", fake, "docker_stats")
	env := newTestEnv(t, "docker_stats")

	rcv, err := factory.CreateMetrics(context.Background(), env.set, factory.CreateDefaultConfig(), consumertest.NewNop())
	require.NoError(t, err)
	require.NoError(t, rcv.Start(context.Background(), componenttest.NewNopHost()))
	defer func() { require.NoError(t, rcv.Shutdown(context.Background())) }()

	logged := env.warnings()[0].ContextMap()["error"].(string)
	assert.NotContains(t, logged, "s3cret")
	assert.NotContains(t, logged, "admin")
	assert.Contains(t, logged, "tcp://***@docker.example:2375")
}

func TestRedactLeavesTextWithoutCredentialsAlone(t *testing.T) {
	assert.Equal(t, "dial unix /var/run/docker.sock: no such file", redact("dial unix /var/run/docker.sock: no such file"))
	assert.Equal(t, "see http://host:2375/path@x", redact("see http://host:2375/path@x"))
}

func TestTracesReceiversAreWrappedToo(t *testing.T) {
	typ := component.MustNewType("tcp_check")
	fake := &fakeReceiver{startErr: errors.New("cannot start")}
	inner := receiver.NewFactory(typ, func() component.Config { return &fakeConfig{} },
		receiver.WithTraces(func(context.Context, receiver.Settings, component.Config, consumer.Traces) (receiver.Traces, error) {
			return fake, nil
		}, component.StabilityLevelDevelopment))
	got := wrapOptionalReceivers(otelcol.Factories{Receivers: map[component.Type]receiver.Factory{typ: inner}}, "tcp_check")
	env := newTestEnv(t, "tcp_check")

	rcv, err := got.Receivers[typ].CreateTraces(context.Background(), env.set, inner.CreateDefaultConfig(), consumertest.NewNop())
	require.NoError(t, err)

	assert.Equal(t, component.StabilityLevelDevelopment, got.Receivers[typ].TracesStability())
	require.NoError(t, rcv.Start(context.Background(), componenttest.NewNopHost()))
	require.NoError(t, rcv.Shutdown(context.Background()))
	assert.Len(t, env.warnings(), 1)
}

// failingMeterProvider returns meters that cannot create a counter.
type failingMeterProvider struct{ noop.MeterProvider }

func (failingMeterProvider) Meter(string, ...otelmetric.MeterOption) otelmetric.Meter {
	return failingMeter{}
}

type failingMeter struct{ noop.Meter }

func (failingMeter) Int64Counter(string, ...otelmetric.Int64CounterOption) (otelmetric.Int64Counter, error) {
	return nil, errors.New("no counters here")
}

func TestSkipIsStillLoggedWhenTheCounterCannotBeCreated(t *testing.T) {
	fake := &fakeReceiver{startErr: errors.New("cannot start")}
	factory := wrapped(t, "docker_stats", fake, "docker_stats")
	env := newTestEnv(t, "docker_stats")
	env.set.MeterProvider = failingMeterProvider{}

	rcv, err := factory.CreateMetrics(context.Background(), env.set, factory.CreateDefaultConfig(), consumertest.NewNop())
	require.NoError(t, err)
	require.NoError(t, rcv.Start(context.Background(), componenttest.NewNopHost()))
	defer func() { require.NoError(t, rcv.Shutdown(context.Background())) }()

	assert.Len(t, env.warnings(), 1)
}
