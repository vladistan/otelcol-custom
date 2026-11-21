// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package ipmireceiver

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/component/componenttest"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/receiver"
	"go.opentelemetry.io/collector/receiver/receivertest"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func settingsWithLogs() (receiver.Settings, *observer.ObservedLogs) {
	core, logs := observer.New(zapcore.DebugLevel)
	set := receivertest.NewNopSettings(component.MustNewType("ipmi"))
	set.Logger = zap.New(core)
	return set, logs
}

func fastConfig(names ...string) *Config {
	cfg := validConfig()
	cfg.CollectionInterval = time.Second
	cfg.BMCs = nil
	for _, n := range names {
		b := validBMC()
		b.Name = n
		cfg.BMCs = append(cfg.BMCs, b)
	}
	return cfg
}

func TestStartOffsetsDifferByNameAndAreStable(t *testing.T) {
	a := startOffset("node12", time.Minute)
	b := startOffset("node13", time.Minute)

	assert.NotEqual(t, a, b)
	assert.Equal(t, a, startOffset("node12", time.Minute))
	assert.Less(t, a, time.Minute)
	assert.GreaterOrEqual(t, a, time.Duration(0))
}

func TestReceiverScrapesEveryBMCAndSendsOneResourceEach(t *testing.T) {
	clock := newClock()
	boards := map[string]*fakeBMC{"a": labBoard(clock, 2, 0, 0), "b": labBoard(clock, 1, 0, 0)}
	sink := new(consumertest.MetricsSink)
	set, _ := settingsWithLogs()
	r := newReceiver(set, fastConfig("a", "b"), sink, func(ctx context.Context, cfg BMCConfig, level string) (bmcSession, error) {
		return boards[cfg.Name].dial(ctx, cfg, level)
	})
	r.offset = func(string, time.Duration) time.Duration { return 0 }

	require.NoError(t, r.Start(context.Background(), componenttest.NewNopHost()))
	assert.Eventually(t, func() bool { return len(sink.AllMetrics()) >= 2 }, 5*time.Second, 20*time.Millisecond)
	require.NoError(t, r.Shutdown(context.Background()))

	hosts := map[string]bool{}
	for _, md := range sink.AllMetrics() {
		require.Equal(t, 1, md.ResourceMetrics().Len())
		host, _ := md.ResourceMetrics().At(0).Resource().Attributes().Get("host.name")
		hosts[host.Str()] = true
	}
	assert.Equal(t, map[string]bool{"a": true, "b": true}, hosts)
}

func TestReceiverScrapesAgainOnEachInterval(t *testing.T) {
	clock := newClock()
	board := labBoard(clock, 1, 0, 0)
	sink := new(consumertest.MetricsSink)
	set, _ := settingsWithLogs()
	r := newReceiver(set, fastConfig("a"), sink, board.dial)
	r.offset = func(string, time.Duration) time.Duration { return 0 }

	require.NoError(t, r.Start(context.Background(), componenttest.NewNopHost()))
	assert.Eventually(t, func() bool { return len(sink.AllMetrics()) >= 2 }, 5*time.Second, 20*time.Millisecond)
	require.NoError(t, r.Shutdown(context.Background()))
}

func TestAtMostMaxConcurrentBMCsAreReadAtOnce(t *testing.T) {
	var running, peak atomic.Int32
	slow := func(ctx context.Context, _ BMCConfig, _ string) (bmcSession, error) {
		n := running.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		select {
		case <-time.After(60 * time.Millisecond):
		case <-ctx.Done():
		}
		running.Add(-1)
		return nil, errRefused
	}
	names := make([]string, 12)
	for i := range names {
		names[i] = fmt.Sprintf("bmc%02d", i)
	}
	cfg := fastConfig(names...)
	cfg.CollectionInterval = time.Hour
	sink := new(consumertest.MetricsSink)
	set, _ := settingsWithLogs()
	r := newReceiver(set, cfg, sink, slow)
	r.offset = func(string, time.Duration) time.Duration { return 0 }

	require.NoError(t, r.Start(context.Background(), componenttest.NewNopHost()))
	assert.Eventually(t, func() bool { return len(sink.AllMetrics()) >= 12 }, 10*time.Second, 20*time.Millisecond)
	require.NoError(t, r.Shutdown(context.Background()))

	assert.LessOrEqual(t, peak.Load(), int32(8))
	assert.Greater(t, peak.Load(), int32(1))
}

func TestAdministratorPrivilegeLogsOneWarnAtStartAndTheDefaultLogsNone(t *testing.T) {
	cfg := fastConfig("quiet", "loud")
	cfg.BMCs[1].PrivilegeLevel = "administrator"
	set, logs := settingsWithLogs()
	r := newReceiver(set, cfg, consumertest.NewNop(), func(context.Context, BMCConfig, string) (bmcSession, error) {
		return nil, errRefused
	})

	require.NoError(t, r.Start(context.Background(), componenttest.NewNopHost()))
	require.NoError(t, r.Shutdown(context.Background()))

	warns := logs.FilterLevelExact(zapcore.WarnLevel).FilterMessageSnippet("privilege")
	assert.Equal(t, 1, warns.Len())

	quietSet, quietLogs := settingsWithLogs()
	q := newReceiver(quietSet, fastConfig("quiet"), consumertest.NewNop(), func(context.Context, BMCConfig, string) (bmcSession, error) {
		return nil, errRefused
	})
	require.NoError(t, q.Start(context.Background(), componenttest.NewNopHost()))
	require.NoError(t, q.Shutdown(context.Background()))
	assert.Zero(t, quietLogs.FilterLevelExact(zapcore.WarnLevel).FilterMessageSnippet("privilege").Len())
}

// privilegeWarnings starts a receiver with the given BMCs and counts the start warnings
// about a privilege level above user, by BMC name.
func privilegeWarnings(t *testing.T, bmcs ...BMCConfig) map[string]int {
	t.Helper()
	cfg := fastConfig()
	cfg.BMCs = bmcs
	set, logs := settingsWithLogs()
	r := newReceiver(set, cfg, consumertest.NewNop(), func(context.Context, BMCConfig, string) (bmcSession, error) {
		return nil, errRefused
	})
	require.NoError(t, r.Start(context.Background(), componenttest.NewNopHost()))
	require.NoError(t, r.Shutdown(context.Background()))

	got := map[string]int{}
	for _, e := range logs.FilterLevelExact(zapcore.WarnLevel).FilterMessageSnippet("more than the user privilege level").All() {
		got[e.ContextMap()["bmc"].(string)]++
	}
	return got
}

// A level above user logs the warning once, unless DCMI power is on: the power reads need an
// administrator account, so the warning would only repeat what the config already says.
func TestPrivilegeWarningFollowsTheLevelAndDCMIPower(t *testing.T) {
	tests := map[string]struct {
		level string
		dcmi  bool
		want  int
	}{
		"user, power off":          {"user", false, 0},
		"user, power on":           {"user", true, 0},
		"operator, power off":      {"operator", false, 1},
		"operator, power on":       {"operator", true, 0},
		"administrator, power off": {"administrator", false, 1},
		"administrator, power on":  {"administrator", true, 0},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			b := validBMC()
			b.Name = "bmc"
			b.PrivilegeLevel = tt.level
			b.DCMIPower = tt.dcmi

			assert.Equal(t, tt.want, privilegeWarnings(t, b)["bmc"])
		})
	}
}

// With two BMCs only the one that has DCMI power off is named in the warning.
func TestPrivilegeWarningNamesOnlyTheBMCWithDCMIPowerOff(t *testing.T) {
	on, off := validBMC(), validBMC()
	on.Name, on.PrivilegeLevel, on.DCMIPower = "power-on", "administrator", true
	off.Name, off.PrivilegeLevel, off.DCMIPower = "power-off", "administrator", false

	got := privilegeWarnings(t, on, off)

	assert.Equal(t, map[string]int{"power-off": 1}, got)
}

type failingSink struct{ calls atomic.Int32 }

func (*failingSink) Capabilities() consumer.Capabilities { return consumer.Capabilities{} }

func (f *failingSink) ConsumeMetrics(context.Context, pmetric.Metrics) error {
	f.calls.Add(1)
	return errors.New("downstream down")
}

func TestReceiverSurvivesAConsumerError(t *testing.T) {
	clock := newClock()
	board := labBoard(clock, 1, 0, 0)
	sink := &failingSink{}
	set, _ := settingsWithLogs()
	r := newReceiver(set, fastConfig("a"), sink, board.dial)
	r.offset = func(string, time.Duration) time.Duration { return 0 }

	require.NoError(t, r.Start(context.Background(), componenttest.NewNopHost()))
	assert.Eventually(t, func() bool { return sink.calls.Load() >= 2 }, 5*time.Second, 20*time.Millisecond)
	require.NoError(t, r.Shutdown(context.Background()))
}

func TestShutdownWithoutStartIsClean(t *testing.T) {
	set, _ := settingsWithLogs()
	r := newReceiver(set, fastConfig("a"), consumertest.NewNop(), nil)

	require.NoError(t, r.Shutdown(context.Background()))
}

func TestShutdownCancelsAScrapeInFlight(t *testing.T) {
	var mu sync.Mutex
	entered := false
	hang := func(ctx context.Context, _ BMCConfig, _ string) (bmcSession, error) {
		mu.Lock()
		entered = true
		mu.Unlock()
		<-ctx.Done()
		return nil, ctx.Err()
	}
	set, _ := settingsWithLogs()
	r := newReceiver(set, fastConfig("a"), consumertest.NewNop(), hang)
	r.offset = func(string, time.Duration) time.Duration { return 0 }

	require.NoError(t, r.Start(context.Background(), componenttest.NewNopHost()))
	assert.Eventually(t, func() bool { mu.Lock(); defer mu.Unlock(); return entered }, 5*time.Second, 10*time.Millisecond)
	done := make(chan struct{})
	go func() { _ = r.Shutdown(context.Background()); close(done) }()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Shutdown did not return while a scrape was in flight")
	}
}
