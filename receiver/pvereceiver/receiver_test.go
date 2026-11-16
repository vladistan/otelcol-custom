// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package pvereceiver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/component/componenttest"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/receiver"
	"go.opentelemetry.io/collector/receiver/receivertest"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func settingsWithLogs() (receiver.Settings, *observer.ObservedLogs) {
	core, logs := observer.New(zapcore.DebugLevel)
	set := receivertest.NewNopSettings(component.MustNewType(typeStr))
	set.Logger = zap.New(core)
	return set, logs
}

func bothEnabled(f *fakePVE) *Config {
	cfg := validConfig()
	cfg.BaseURLs = []string{f.url()}
	cfg.Resources.Timeout = time.Second
	cfg.Guests = GuestsConfig{ScraperConfig: ScraperConfig{
		Enabled: true, TokenID: "otel@pve!guests", TokenSecret: "guests-secret",
		CollectionInterval: time.Hour, Timeout: time.Second,
	}, Node: "pve1"}
	return cfg
}

func allLogText(logs *observer.ObservedLogs) string {
	var b strings.Builder
	for _, e := range logs.All() {
		b.WriteString(e.Message + " ")
		for k, v := range e.ContextMap() {
			b.WriteString(k + "=" + fmt.Sprint(v) + " ")
		}
	}
	return b.String()
}

func TestStartMakesNoNetworkCall(t *testing.T) {
	f := resourcesFake(t)
	old := initialDelay
	initialDelay = time.Hour
	t.Cleanup(func() { initialDelay = old })
	set, _ := settingsWithLogs()
	r, err := newReceiver(set, bothEnabled(f))
	require.NoError(t, err)
	r.setMetrics(consumertest.NewNop())

	require.NoError(t, r.Start(context.Background(), componenttest.NewNopHost()))

	assert.Empty(t, f.hitList())
	require.NoError(t, r.Shutdown(context.Background()))
}

func TestWithTheAPIDownTheCollectorStartsAndEachScrapeLogsOneError(t *testing.T) {
	dead := httptest.NewServer(http.NotFoundHandler())
	deadURL := dead.URL
	dead.Close()
	set, logs := settingsWithLogs()
	cfg := validConfig()
	cfg.BaseURLs = []string{deadURL}
	cfg.Resources.Timeout = 200 * time.Millisecond
	sink := new(consumertest.MetricsSink)
	r, err := newReceiver(set, cfg)
	require.NoError(t, err)
	r.setMetrics(sink)
	old := initialDelay
	initialDelay = time.Hour
	t.Cleanup(func() { initialDelay = old })

	require.NoError(t, r.Start(context.Background(), componenttest.NewNopHost()), "start with the API down")
	r.scrapeResourcesOnce(context.Background())
	r.scrapeResourcesOnce(context.Background())

	assert.Len(t, logs.FilterLevelExact(zapcore.ErrorLevel).All(), 2, "one error per interval")
	assert.Zero(t, sink.DataPointCount(), "no data")
	require.NoError(t, r.Shutdown(context.Background()))
}

func TestBothScrapersFeedTheirConsumers(t *testing.T) {
	f := resourcesFake(t)
	for p, rt := range guestsFake(t).routes {
		f.routes[p] = rt
	}
	set, _ := settingsWithLogs()
	cfg := bothEnabled(f)
	metrics, logs := new(consumertest.MetricsSink), new(consumertest.LogsSink)
	r, err := newReceiver(set, cfg)
	require.NoError(t, err)
	r.setMetrics(metrics)
	r.setLogs(logs)

	r.scrapeResourcesOnce(context.Background())
	r.sweepGuestsOnce(context.Background())

	assert.NotZero(t, metrics.DataPointCount())
	assert.NotZero(t, logs.LogRecordCount())
	assert.Len(t, metrics.AllMetrics(), 2, "one batch per scraper")
}

func TestOnlyResourcesEnabledStartsNoGuestsScraper(t *testing.T) {
	f := resourcesFake(t)
	cfg := validConfig()
	cfg.BaseURLs = []string{f.url()}
	set, _ := settingsWithLogs()

	r, err := newReceiver(set, cfg)

	require.NoError(t, err)
	assert.NotNil(t, r.resources)
	assert.Nil(t, r.guests)
	r.setMetrics(consumertest.NewNop())
	old := initialDelay
	initialDelay = time.Millisecond
	t.Cleanup(func() { initialDelay = old })
	require.NoError(t, r.Start(context.Background(), componenttest.NewNopHost()))
	require.Eventually(t, func() bool { return f.countOf("/api2/json/cluster/resources") > 0 }, 5*time.Second, 5*time.Millisecond)
	require.NoError(t, r.Shutdown(context.Background()))
	for _, hit := range f.hitList() {
		assert.NotContains(t, hit, "/nodes/")
	}
}

func TestIntervalsRepeatTheScrape(t *testing.T) {
	f := resourcesFake(t)
	cfg := validConfig()
	cfg.BaseURLs = []string{f.url()}
	cfg.Resources.CollectionInterval = 20 * time.Millisecond
	set, _ := settingsWithLogs()
	r, err := newReceiver(set, cfg)
	require.NoError(t, err)
	r.setMetrics(consumertest.NewNop())
	old := initialDelay
	initialDelay = time.Millisecond
	t.Cleanup(func() { initialDelay = old })

	require.NoError(t, r.Start(context.Background(), componenttest.NewNopHost()))
	require.Eventually(t, func() bool { return f.countOf("/api2/json/cluster/resources") >= 3 }, 5*time.Second, 5*time.Millisecond)
	require.NoError(t, r.Shutdown(context.Background()))
}

func TestTwoPipelinesShareOneInstanceAndItStartsOnce(t *testing.T) {
	f := resourcesFake(t)
	cfg := bothEnabled(f)
	set, _ := settingsWithLogs()
	factory := NewFactory()

	m, err := factory.CreateMetrics(context.Background(), set, cfg, consumertest.NewNop())
	require.NoError(t, err)
	l, err := factory.CreateLogs(context.Background(), set, cfg, consumertest.NewNop())
	require.NoError(t, err)

	assert.Same(t, m, l, "one receiver for both signals")
	old := initialDelay
	initialDelay = time.Hour
	t.Cleanup(func() { initialDelay = old })
	require.NoError(t, m.Start(context.Background(), componenttest.NewNopHost()))
	require.NoError(t, l.Start(context.Background(), componenttest.NewNopHost()))
	require.NoError(t, m.Shutdown(context.Background()))
	require.NoError(t, l.Shutdown(context.Background()))
}

func TestSecretIsInNoLogLineErrorOrConfigDumpFor401403AndTLS(t *testing.T) {
	cases := map[string]func(*fakePVE){
		"401": func(f *fakePVE) { f.set("/api2/json/cluster/resources", http.StatusUnauthorized, "bad "+testSecret) },
		"403": func(f *fakePVE) { f.set("/api2/json/cluster/resources", http.StatusForbidden, "denied "+testSecret) },
	}
	for name, setup := range cases {
		f := resourcesFake(t)
		setup(f)
		cfg := validConfig()
		cfg.BaseURLs = []string{f.url()}
		cfg.Resources.TokenSecret = testSecret // pragma: allowlist secret
		set, logs := settingsWithLogs()
		r, err := newReceiver(set, cfg)
		require.NoError(t, err)
		r.setMetrics(consumertest.NewNop())

		r.scrapeResourcesOnce(context.Background())

		assert.NotEmpty(t, logs.All(), name)
		assert.NotContains(t, allLogText(logs), testSecret, name)
		assert.NotContains(t, fmt.Sprintf("%v %+v %#v", cfg, cfg, cfg), testSecret, name+" config dump")
	}

	tlsFake := newFakePVE(t, true)
	cfg := validConfig()
	cfg.BaseURLs = []string{tlsFake.url()}
	cfg.Resources.TokenSecret = testSecret // pragma: allowlist secret
	set, logs := settingsWithLogs()
	r, err := newReceiver(set, cfg)
	require.NoError(t, err)
	r.setMetrics(consumertest.NewNop())
	r.scrapeResourcesOnce(context.Background())
	assert.NotContains(t, allLogText(logs), testSecret, "tls")
	assert.Len(t, logs.FilterLevelExact(zapcore.ErrorLevel).All(), 1, "one error for the verification failure")
}

func TestA401GivesOneWarnPerInterval(t *testing.T) {
	f := resourcesFake(t)
	f.set("/api2/json/cluster/resources", http.StatusUnauthorized, "no")
	cfg := validConfig()
	cfg.BaseURLs = []string{f.url()}
	set, logs := settingsWithLogs()
	r, err := newReceiver(set, cfg)
	require.NoError(t, err)
	r.setMetrics(consumertest.NewNop())

	r.scrapeResourcesOnce(context.Background())
	r.scrapeResourcesOnce(context.Background())

	assert.Len(t, logs.FilterLevelExact(zapcore.WarnLevel).All(), 2)
}

func TestA403OnTheGuestAgentGivesOneWarnNamingThePrivilege(t *testing.T) {
	for _, priv := range []string{"VM.Monitor", "VM.GuestAgent.Audit"} {
		f := guestsFake(t)
		f.set(prefix+"/qemu/100/agent/get-osinfo", http.StatusForbidden, "Permission check failed (/vms/100, "+priv+")")
		cfg := bothEnabled(f)
		cfg.Resources.Enabled = false
		set, logs := settingsWithLogs()
		r, err := newReceiver(set, cfg)
		require.NoError(t, err)
		r.setMetrics(consumertest.NewNop())
		r.setLogs(consumertest.NewNop())

		r.sweepGuestsOnce(context.Background())

		warns := logs.FilterLevelExact(zapcore.WarnLevel).All()
		require.Len(t, warns, 1, priv)
		assert.Contains(t, allLogText(logs), priv)
		assert.Contains(t, allLogText(logs), "VM.Audit")
	}
}

func TestFactoryRefusesAnInvalidConfig(t *testing.T) {
	set, _ := settingsWithLogs()
	_, err := NewFactory().CreateMetrics(context.Background(), set, createDefaultConfig(), consumertest.NewNop())
	assert.Error(t, err)
	_, err = NewFactory().CreateLogs(context.Background(), set, createDefaultConfig(), consumertest.NewNop())
	assert.Error(t, err)
}

func TestFactoryTypeAndStability(t *testing.T) {
	factory := NewFactory()

	assert.Equal(t, component.MustNewType("pve"), factory.Type())
	assert.Equal(t, component.StabilityLevelDevelopment, factory.MetricsStability())
	assert.Equal(t, component.StabilityLevelDevelopment, factory.LogsStability())
}

func TestWithoutALogsConsumerTheFactsAreSkippedQuietly(t *testing.T) {
	f := guestsFake(t)
	cfg := bothEnabled(f)
	cfg.Resources.Enabled = false
	set, _ := settingsWithLogs()
	r, err := newReceiver(set, cfg)
	require.NoError(t, err)
	sink := new(consumertest.MetricsSink)
	r.setMetrics(sink)

	r.sweepGuestsOnce(context.Background())

	assert.NotZero(t, sink.DataPointCount())
}

func TestSourceHasNoExecAndNoCgo(t *testing.T) {
	files, err := filepath.Glob("*.go")
	require.NoError(t, err)
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		require.NoError(t, err)
		assert.NotContains(t, string(src), `"os/exec"`, name)
		assert.NotContains(t, string(src), `import "C"`, name)
	}
}

func TestAnEmptyGuestsNodeWithAnUnknownHostNameIsRefused(t *testing.T) {
	old := hostnameFn
	hostnameFn = func() (string, error) { return "", errors.New("no host name") }
	t.Cleanup(func() { hostnameFn = old })
	cfg := validConfig()
	cfg.Resources.Enabled = false
	cfg.Guests = GuestsConfig{ScraperConfig: ScraperConfig{
		Enabled: true, TokenID: "otel@pve!guests", TokenSecret: "s",
		CollectionInterval: time.Hour, Timeout: time.Second,
	}}
	set, _ := settingsWithLogs()

	_, err := newReceiver(set, cfg)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "guests.node")
}

func TestInsecureSkipVerifyLogsOneWarnAtStart(t *testing.T) {
	f := resourcesFake(t)
	cfg := validConfig()
	cfg.BaseURLs = []string{f.url()}
	cfg.InsecureSkipVerify = true
	set, logs := settingsWithLogs()
	r, err := newReceiver(set, cfg)
	require.NoError(t, err)
	r.setMetrics(consumertest.NewNop())
	old := initialDelay
	initialDelay = time.Hour
	t.Cleanup(func() { initialDelay = old })

	require.NoError(t, r.Start(context.Background(), componenttest.NewNopHost()))
	require.NoError(t, r.Shutdown(context.Background()))

	warns := logs.FilterLevelExact(zapcore.WarnLevel).All()
	require.Len(t, warns, 1)
	assert.Contains(t, warns[0].Message, "insecure_skip_verify")
}

func TestShutdownClosesTheIdleConnections(t *testing.T) {
	f := resourcesFake(t)
	cfg := validConfig()
	cfg.BaseURLs = []string{f.url()}
	set, _ := settingsWithLogs()
	r, err := newReceiver(set, cfg)
	require.NoError(t, err)
	r.setMetrics(consumertest.NewNop())
	r.scrapeResourcesOnce(context.Background())
	require.Zero(t, f.closedConns.Load())
	old := initialDelay
	initialDelay = time.Hour
	t.Cleanup(func() { initialDelay = old })
	require.NoError(t, r.Start(context.Background(), componenttest.NewNopHost()))

	require.NoError(t, r.Shutdown(context.Background()))

	require.Eventually(t, func() bool { return f.closedConns.Load() >= 1 }, 5*time.Second, 5*time.Millisecond)
}
