// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package cloudwatchmetricsreceiver

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/component/componenttest"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/extension/xextension/storage"
	"go.opentelemetry.io/collector/receiver/receivertest"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

type env struct {
	t       *testing.T
	cfg     *Config
	r       *cwReceiver
	sink    *consumertest.MetricsSink
	logs    *observer.ObservedLogs
	reader  *sdkmetric.ManualReader
	clients *fakeClients
	store   stateStore
	clock   time.Time
	pairs   []*pairState
}

func dailyConfig() *Config {
	return &Config{
		MaxCatchup: 72 * time.Hour,
		Accounts:   []AccountConfig{{Name: "prod", Regions: []string{"us-east-1"}}},
		Tiers: map[string]TierConfig{"daily": {
			CollectionInterval: 24 * time.Hour, Period: 24 * time.Hour, Delay: time.Hour,
			Stats: []string{"Average", "p50"}, Limit: 10,
			Namespaces: map[string][]string{"AWS/EC2": {"CPUUtilization"}},
		}},
	}
}

func opConfig() *Config {
	cfg := dailyConfig()
	cfg.Tiers = map[string]TierConfig{"operational": {
		CollectionInterval: 15 * time.Minute, Period: 15 * time.Minute, Delay: 10 * time.Minute,
		Stats: []string{"Average", "Maximum"}, Limit: 10,
		Namespaces: map[string][]string{"AWS/EC2": {"CPUUtilization"}},
	}}
	return cfg
}

func oneInstance(account, region string) *fakeAPI {
	return newFakeAPI().add("AWS/EC2", "CPUUtilization", "InstanceId", account+"-"+region)
}

func newEnv(t *testing.T, cfg *Config) *env {
	t.Helper()
	core, logs := observer.New(zapcore.DebugLevel)
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	settings := receivertest.NewNopSettings(component.MustNewType(typeStr))
	settings.Logger = zap.New(core)
	settings.MeterProvider = provider

	e := &env{t: t, cfg: cfg, sink: new(consumertest.MetricsSink), logs: logs, reader: reader,
		clients: newFakeClients(oneInstance), store: newMemoryState(), clock: utc(15, 2, 0)}
	e.r = newReceiver(settings, cfg, e.sink)
	e.r.clients = e.clients
	e.r.store = e.store
	e.r.now = func() time.Time { return e.clock }
	return e
}

// pollAll polls every pair once; the pairs live as long as the env, like in a run.
func (e *env) pollAll() {
	e.t.Helper()
	if e.pairs == nil {
		e.pairs = e.r.newPairs()
	}
	for _, p := range e.pairs {
		e.r.poll(context.Background(), p)
	}
}

func (e *env) key(account, region, tier string) string { return stateKey(account, region, tier) }

func (e *env) saved(account, region, tier string) (time.Time, bool) {
	return e.store.Get(context.Background(), e.key(account, region, tier))
}

func (e *env) messages(level zapcore.Level) []string {
	var out []string
	for _, entry := range e.logs.FilterLevelExact(level).All() {
		out = append(out, entry.Message)
	}
	return out
}

func (e *env) gauges(name string) map[string]int64 {
	e.t.Helper()
	var rm metricdata.ResourceMetrics
	require.NoError(e.t, e.reader.Collect(context.Background(), &rm))
	out := map[string]int64{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != name {
				continue
			}
			for _, dp := range m.Data.(metricdata.Gauge[int64]).DataPoints {
				account, _ := dp.Attributes.Value("account")
				region, _ := dp.Attributes.Value("region")
				tier, _ := dp.Attributes.Value("tier")
				out[fmt.Sprintf("%s/%s/%s", account.AsString(), region.AsString(), tier.AsString())] = dp.Value
			}
		}
	}
	return out
}

func (e *env) resources() map[string]bool {
	seen := map[string]bool{}
	for _, md := range e.sink.AllMetrics() {
		for i := 0; i < md.ResourceMetrics().Len(); i++ {
			attrs := md.ResourceMetrics().At(i).Resource().Attributes()
			account, _ := attrs.Get("cloud.account.name")
			region, _ := attrs.Get("cloud.region")
			seen[account.Str()+"/"+region.Str()] = true
		}
	}
	return seen
}

func (e *env) pointCount() int { return e.sink.DataPointCount() }

func TestPollEveryAccountAndRegionPairIsReadAndMarkedWithItsAccount(t *testing.T) {
	cfg := opConfig()
	cfg.Accounts = []AccountConfig{
		{Name: "prod", Regions: []string{"us-east-1", "eu-west-1"}},
		{Name: "tools", Regions: []string{"us-east-1", "eu-west-1"}},
	}
	e := newEnv(t, cfg)

	e.pollAll()

	assert.Equal(t, map[string]bool{"prod/us-east-1": true, "prod/eu-west-1": true, "tools/us-east-1": true, "tools/eu-west-1": true}, e.resources())
}

func TestPollPeriodAndDelayReachTheRequestWindow(t *testing.T) {
	e := newEnv(t, opConfig())
	e.clock = utc(15, 14, 7)

	e.pollAll()

	call := e.clients.api("prod", "us-east-1").data()[0]
	assert.True(t, utc(15, 13, 30).Equal(call.start), "start %v", call.start)
	assert.True(t, utc(15, 13, 45).Equal(call.end), "end %v", call.end)
	assert.Equal(t, int32(900), call.period)
}

func TestPollTruncatedDiscoveryReportsTheGaugesAndQueriesOnlyTheLimit(t *testing.T) {
	cfg := opConfig()
	e := newEnv(t, cfg)
	e.clients.build = func(string, string) *fakeAPI { return twelveMetrics() }

	e.pollAll()

	key := "prod/us-east-1/operational"
	assert.Equal(t, map[string]int64{key: 12}, e.gauges("otelcol_aws_cloudwatch_series_discovered"))
	assert.Equal(t, map[string]int64{key: 10}, e.gauges("otelcol_aws_cloudwatch_series_limit"))
	assert.Equal(t, map[string]int64{key: 1}, e.gauges("otelcol_aws_cloudwatch_discovery_truncated"))
	total := 0
	for _, c := range e.clients.api("prod", "us-east-1").data() {
		total += c.queries
	}
	assert.Equal(t, 20, total, "10 series with 2 stats, not 12")
}

func TestPollUntruncatedDiscoveryReportsZero(t *testing.T) {
	e := newEnv(t, opConfig())

	e.pollAll()

	assert.Equal(t, map[string]int64{"prod/us-east-1/operational": 0}, e.gauges("otelcol_aws_cloudwatch_discovery_truncated"))
}

func TestPollTruncationWarnsOncePerEpisode(t *testing.T) {
	e := newEnv(t, opConfig())
	e.clients.build = func(string, string) *fakeAPI { return twelveMetrics() }

	e.pollAll()
	e.clock = e.clock.Add(15 * time.Minute)
	e.pollAll()
	assert.Len(t, e.messages(zapcore.WarnLevel), 1, "two truncated polls in a row warn once")

	e.clients.api("prod", "us-east-1").metrics["AWS/EC2|CPUUtilization"] = e.clients.api("prod", "us-east-1").metrics["AWS/EC2|CPUUtilization"][:5]
	e.clock = e.clock.Add(15 * time.Minute)
	e.pollAll()
	assert.Len(t, e.messages(zapcore.WarnLevel), 1, "an untruncated poll does not warn")

	e.clients.api("prod", "us-east-1").metrics["AWS/EC2|CPUUtilization"] = twelveMetrics().metrics["AWS/EC2|CPUUtilization"]
	e.clock = e.clock.Add(15 * time.Minute)
	e.pollAll()
	assert.Len(t, e.messages(zapcore.WarnLevel), 2, "a truncated poll after an untruncated one warns again")
}

func TestPollSavesTheWindowEnd(t *testing.T) {
	e := newEnv(t, dailyConfig())

	e.pollAll()

	got, ok := e.saved("prod", "us-east-1", "daily")
	require.True(t, ok)
	assert.True(t, utc(15, 0, 0).Equal(got))
}

func TestPollRestartInsideTheSameDayDoesNotReadTheWindowAgain(t *testing.T) {
	e := newEnv(t, dailyConfig())
	e.pollAll()
	before := len(e.clients.api("prod", "us-east-1").data())

	e.clock = utc(15, 14, 0)
	restarted := newReceiver(receivertest.NewNopSettings(component.MustNewType(typeStr)), e.cfg, e.sink)
	restarted.clients, restarted.store, restarted.now = e.clients, e.store, func() time.Time { return e.clock }
	for _, p := range restarted.newPairs() {
		restarted.poll(context.Background(), p)
	}

	assert.Len(t, e.clients.api("prod", "us-east-1").data(), before, "no new GetMetricData call")
}

func windowEnds(calls []dataCall) []time.Time {
	var out []time.Time
	for _, c := range calls {
		out = append(out, c.end)
	}
	return out
}

func TestPollAfterATwoDayStopReadsBothMissedDaysOldestFirst(t *testing.T) {
	e := newEnv(t, dailyConfig())
	require.NoError(t, e.store.Set(context.Background(), e.key("prod", "us-east-1", "daily"), utc(13, 0, 0)))
	e.clock = utc(15, 2, 0)

	e.pollAll()

	assert.Equal(t, []time.Time{utc(14, 0, 0), utc(15, 0, 0)}, windowEnds(e.clients.api("prod", "us-east-1").data()))
}

func TestPollAfterAFiveDayStopReadsTheLastThreeDaysOnly(t *testing.T) {
	e := newEnv(t, dailyConfig())
	require.NoError(t, e.store.Set(context.Background(), e.key("prod", "us-east-1", "daily"), utc(10, 0, 0)))
	e.clock = utc(15, 2, 0)

	e.pollAll()

	assert.Equal(t, []time.Time{utc(13, 0, 0), utc(14, 0, 0), utc(15, 0, 0)}, windowEnds(e.clients.api("prod", "us-east-1").data()))
}

func TestPollDailyTierAtTwoOClockReadsThePreviousUTCDay(t *testing.T) {
	e := newEnv(t, dailyConfig())
	e.clock = utc(15, 2, 0)

	e.pollAll()

	call := e.clients.api("prod", "us-east-1").data()[0]
	assert.True(t, utc(14, 0, 0).Equal(call.start))
	assert.True(t, utc(15, 0, 0).Equal(call.end))
}

func TestPollWithoutStateReadsThePreviousCompleteWindowOnly(t *testing.T) {
	e := newEnv(t, dailyConfig())

	e.pollAll()

	assert.Len(t, e.clients.api("prod", "us-east-1").data(), 1)
}

func TestPollNoMatchingMetricEmitsNothingLogsNoErrorAndAdvances(t *testing.T) {
	e := newEnv(t, opConfig())
	e.clients.build = func(string, string) *fakeAPI { return newFakeAPI() }

	e.pollAll()

	assert.Equal(t, 0, e.pointCount())
	assert.Empty(t, e.messages(zapcore.WarnLevel))
	assert.Empty(t, e.messages(zapcore.ErrorLevel))
	_, ok := e.saved("prod", "us-east-1", "operational")
	assert.True(t, ok, "the empty window is done")
}

func TestPollRefusedRoleWarnsOncePerPairAndTheOthersContinue(t *testing.T) {
	cfg := opConfig()
	cfg.Accounts = []AccountConfig{
		{Name: "prod", Regions: []string{"us-east-1"}},
		{Name: "tools", Regions: []string{"us-east-1"}},
	}
	e := newEnv(t, cfg)
	e.clients.fail["tools"] = errors.New("AccessDenied")

	e.pollAll()
	e.clock = e.clock.Add(15 * time.Minute)
	e.pollAll()

	assert.Equal(t, map[string]bool{"prod/us-east-1": true}, e.resources())
	warnings := e.logs.FilterLevelExact(zapcore.WarnLevel).All()
	require.Len(t, warnings, 1, "one WARN over two polls")
	assert.Equal(t, "tools", warnings[0].ContextMap()["account"])
	assert.Equal(t, "us-east-1", warnings[0].ContextMap()["region"])
}

func TestPollRefusedRoleIsTriedAgainAtTheNextPollAndRecovers(t *testing.T) {
	cfg := opConfig()
	cfg.Accounts = []AccountConfig{{Name: "tools", Regions: []string{"us-east-1"}}}
	e := newEnv(t, cfg)
	e.clients.fail["tools"] = errors.New("AccessDenied")
	e.pollAll()

	delete(e.clients.fail, "tools")
	e.clock = e.clock.Add(15 * time.Minute)
	e.pollAll()

	assert.Equal(t, map[string]bool{"tools/us-east-1": true}, e.resources())
}

func TestPollConsumerErrorKeepsTheWindowForTheNextPoll(t *testing.T) {
	e := newEnv(t, opConfig())
	e.r.consumer = consumertest.NewErr(errors.New("downstream refused"))

	e.pollAll()
	_, saved := e.saved("prod", "us-east-1", "operational")
	assert.False(t, saved, "a window that was not delivered is not recorded")
	assert.NotEmpty(t, e.messages(zapcore.ErrorLevel))

	e.r.consumer = e.sink
	e.pollAll()

	assert.Positive(t, e.pointCount())
}

func TestPollGetMetricDataErrorWarnsAndKeepsTheState(t *testing.T) {
	e := newEnv(t, opConfig())
	e.clients.build = func(a, r string) *fakeAPI {
		api := oneInstance(a, r)
		api.dataErr = errors.New("throttled")
		return api
	}

	e.pollAll()

	assert.Len(t, e.messages(zapcore.WarnLevel), 1)
	_, saved := e.saved("prod", "us-east-1", "operational")
	assert.False(t, saved)
	assert.Equal(t, 0, e.pointCount())
}

func TestPollListMetricsErrorWarnsAndKeepsTheState(t *testing.T) {
	e := newEnv(t, opConfig())
	e.clients.build = func(a, r string) *fakeAPI {
		api := oneInstance(a, r)
		api.listErr = errors.New("throttled")
		return api
	}

	e.pollAll()

	assert.Len(t, e.messages(zapcore.WarnLevel), 1)
	_, saved := e.saved("prod", "us-east-1", "operational")
	assert.False(t, saved)
}

func TestPollPercentileWithoutSupportLogsOneInfoPerMetricNamePerRun(t *testing.T) {
	e := newEnv(t, dailyConfig())
	e.clients.build = func(a, r string) *fakeAPI {
		api := oneInstance(a, r)
		api.noData = func(stat, _, _ string) bool { return stat == "p50" }
		return api
	}

	for i := 0; i < 3; i++ {
		e.pollAll()
		e.clock = e.clock.Add(24 * time.Hour)
	}

	assert.Empty(t, e.messages(zapcore.ErrorLevel))
	assert.Empty(t, e.messages(zapcore.WarnLevel))
	infos := e.logs.FilterLevelExact(zapcore.InfoLevel).FilterMessageSnippet("percentile").All()
	require.Len(t, infos, 1, "three p50 requests, one INFO line")
	assert.Equal(t, "AWS/EC2/CPUUtilization", infos[0].ContextMap()["metric"])
	for _, md := range e.sink.AllMetrics() {
		ms := md.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics()
		for i := 0; i < ms.Len(); i++ {
			dps := ms.At(i).Gauge().DataPoints()
			for j := 0; j < dps.Len(); j++ {
				stat, _ := dps.At(j).Attributes().Get("stat")
				assert.NotEqual(t, "p50", stat.Str())
			}
		}
	}
}

func TestPollPercentileWithSupportEmitsItWithoutALog(t *testing.T) {
	e := newEnv(t, dailyConfig())

	e.pollAll()

	assert.Empty(t, e.logs.FilterMessageSnippet("percentile").All())
	assert.Equal(t, 2, e.pointCount(), "Average and p50 for the one series")
}

func TestPollsEachTierOfAnAccountAndRegionSeparately(t *testing.T) {
	cfg := dailyConfig()
	cfg.Tiers["operational"] = opConfig().Tiers["operational"]
	e := newEnv(t, cfg)

	e.pollAll()

	_, daily := e.saved("prod", "us-east-1", "daily")
	_, operational := e.saved("prod", "us-east-1", "operational")
	assert.True(t, daily)
	assert.True(t, operational)
	assert.Len(t, e.r.newPairs(), 2)
}

// fakeHost serves a storage extension under one ID.
type fakeHost struct {
	component.Host
	id  component.ID
	ext component.Component
}

func (h fakeHost) GetExtensions() map[component.ID]component.Component {
	return map[component.ID]component.Component{h.id: h.ext}
}

type fakeStorageExtension struct {
	component.Component
	client *fakeStorageClient
}

func (f fakeStorageExtension) Start(context.Context, component.Host) error { return nil }
func (f fakeStorageExtension) Shutdown(context.Context) error              { return nil }
func (f fakeStorageExtension) GetClient(context.Context, component.Kind, component.ID, string) (storage.Client, error) {
	return f.client, nil
}

func TestStartKeepsTheStateInTheNamedStorageExtensionAndShutdownClosesIt(t *testing.T) {
	cfg := opConfig()
	id := component.MustNewID("file_storage")
	cfg.StorageID = &id
	cfg.Tiers["operational"] = TierConfig{
		CollectionInterval: time.Second, Period: time.Second, Delay: 0, Stats: []string{"Average"}, Limit: 10,
		Namespaces: map[string][]string{"AWS/EC2": {"CPUUtilization"}},
	}
	e := newEnv(t, cfg)
	client := newFakeStorageClient()
	host := fakeHost{Host: componenttest.NewNopHost(), id: id, ext: fakeStorageExtension{client: client}}
	e.r.store = nil
	e.r.now = time.Now

	require.NoError(t, e.r.Start(context.Background(), host))
	require.Eventually(t, func() bool { return client.count() > 0 }, 3*time.Second, 20*time.Millisecond)
	require.NoError(t, e.r.Shutdown(context.Background()))

	assert.True(t, client.has(stateKey("prod", "us-east-1", "operational")))
	assert.True(t, client.isClosed())
	assert.Positive(t, e.pointCount())
}

func TestStartWithoutTheNamedStorageExtensionFails(t *testing.T) {
	cfg := opConfig()
	id := component.MustNewID("file_storage")
	cfg.StorageID = &id
	e := newEnv(t, cfg)

	err := e.r.Start(context.Background(), componenttest.NewNopHost())

	assert.ErrorContains(t, err, "file_storage")
}

func TestStartWithAnExtensionThatIsNotStorageFails(t *testing.T) {
	cfg := opConfig()
	id := component.MustNewID("health_check")
	cfg.StorageID = &id
	e := newEnv(t, cfg)
	host := fakeHost{Host: componenttest.NewNopHost(), id: id, ext: struct{ component.Component }{}}

	err := e.r.Start(context.Background(), host)

	assert.ErrorContains(t, err, "not a storage extension")
}

func TestStartWithoutStorageUsesMemoryStateAndShutdownIsClean(t *testing.T) {
	cfg := opConfig()
	cfg.Tiers["operational"] = TierConfig{
		CollectionInterval: time.Second, Period: time.Second, Stats: []string{"Average"}, Limit: 10,
		Namespaces: map[string][]string{"AWS/EC2": {"CPUUtilization"}},
	}
	e := newEnv(t, cfg)
	e.r.store = nil
	e.r.now = time.Now

	require.NoError(t, e.r.Start(context.Background(), componenttest.NewNopHost()))
	require.Eventually(t, func() bool { return e.pointCount() > 0 }, 3*time.Second, 20*time.Millisecond)

	assert.NoError(t, e.r.Shutdown(context.Background()))
}

func TestShutdownBeforeStartIsSafe(t *testing.T) {
	e := newEnv(t, opConfig())

	assert.NoError(t, e.r.Shutdown(context.Background()))
}

var _ consumer.Metrics = (*consumertest.MetricsSink)(nil)
