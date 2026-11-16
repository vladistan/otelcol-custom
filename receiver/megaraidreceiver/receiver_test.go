// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package megaraidreceiver

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/component/componenttest"
	"go.opentelemetry.io/collector/confmap"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/extension/xextension/storage"
	"go.opentelemetry.io/collector/receiver/receivertest"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func TestDefaultsAndKeysOfTheConfig(t *testing.T) {
	cfg := createDefaultConfig().(*Config)

	assert.Equal(t, time.Minute, cfg.CollectionInterval)
	assert.False(t, cfg.Smart)
	assert.Equal(t, 15*time.Minute, cfg.SmartInterval)
	assert.False(t, cfg.CreateNode)
	assert.True(t, cfg.Events.Enabled)
	assert.Equal(t, "info", cfg.Events.MinClass)
	assert.Equal(t, 200, cfg.Events.MaxPerScrape)
	require.NoError(t, cfg.Validate())

	cm := confmap.NewFromStringMap(map[string]any{
		"collection_interval": "30s", "smart": true, "smart_interval": "5m", "create_node": true,
		"events": map[string]any{"enabled": true, "storage": "file_storage/megaraid", "min_class": "warning", "max_per_scrape": 50},
	})
	require.NoError(t, cm.Unmarshal(cfg))
	require.NoError(t, cfg.Validate())
	assert.Equal(t, 30*time.Second, cfg.CollectionInterval)
	assert.True(t, cfg.Smart)
	assert.True(t, cfg.CreateNode)
	assert.Equal(t, "file_storage/megaraid", cfg.Events.Storage.String())
	assert.Equal(t, "warning", cfg.Events.MinClass)
	assert.Equal(t, 50, cfg.Events.MaxPerScrape)
}

func TestAStorageKeyThatNamesAMissingExtensionIsNotAValidateError(t *testing.T) {
	cfg := createDefaultConfig().(*Config)
	id := component.MustNewID("file_storage")
	cfg.Events.Storage = &id

	assert.NoError(t, cfg.Validate())
}

func TestInvalidKeysFailValidate(t *testing.T) {
	tests := map[string]func(*Config){
		"interval":       func(c *Config) { c.CollectionInterval = time.Millisecond },
		"smart interval": func(c *Config) { c.Smart = true; c.SmartInterval = time.Second },
		"class":          func(c *Config) { c.Events.MinClass = "loud" },
		"max low":        func(c *Config) { c.Events.MaxPerScrape = 0 },
		"max high":       func(c *Config) { c.Events.MaxPerScrape = maxMaxPerScrape + 1 },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			cfg := createDefaultConfig().(*Config)
			mutate(cfg)

			assert.Error(t, cfg.Validate())
		})
	}
	cfg := createDefaultConfig().(*Config)
	assert.Error(t, confmap.NewFromStringMap(map[string]any{"events": map[string]any{"enable": true}}).Unmarshal(cfg))
}

func TestFactoryRefusesOtherSystemsAndBadConfigs(t *testing.T) {
	defer func(old string) { goos = old }(goos)
	f := NewFactory()
	assert.Equal(t, "megaraid", f.Type().String())
	set := receivertest.NewNopSettings(f.Type())

	goos = "freebsd"
	_, err := f.CreateMetrics(context.Background(), set, f.CreateDefaultConfig(), consumertest.NewNop())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "only on Linux")
	_, err = f.CreateLogs(context.Background(), set, f.CreateDefaultConfig(), consumertest.NewNop())
	assert.Error(t, err)

	goos = "linux"
	bad := f.CreateDefaultConfig().(*Config)
	bad.Events.MinClass = "loud"
	_, err = f.CreateMetrics(context.Background(), set, bad, consumertest.NewNop())
	assert.Error(t, err)
}

func TestMetricsAndLogsOfOneConfigShareOneReceiver(t *testing.T) {
	defer func(old string) { goos = old }(goos)
	goos = "linux"
	f := NewFactory()
	set := receivertest.NewNopSettings(f.Type())
	cfg := f.CreateDefaultConfig()

	m, err := f.CreateMetrics(context.Background(), set, cfg, consumertest.NewNop())
	require.NoError(t, err)
	l, err := f.CreateLogs(context.Background(), set, cfg, consumertest.NewNop())
	require.NoError(t, err)

	assert.Same(t, m, l)
	r := m.(*megaReceiver)
	r.firstDelay = time.Hour
	r.cfg.Events.Enabled = false
	require.NoError(t, m.Start(context.Background(), componenttest.NewNopHost()))
	require.NoError(t, l.Start(context.Background(), componenttest.NewNopHost()))
	assert.Equal(t, 2, r.started)
	require.NoError(t, m.Shutdown(context.Background()))
	assert.Equal(t, 1, r.started, "the loop runs until the last pipeline stops")
	require.NoError(t, l.Shutdown(context.Background()))
	assert.NoError(t, l.Shutdown(context.Background()), "a second shutdown is harmless")
}

// ---- events and storage ----

type fakeHost struct {
	component.Host
	ext map[component.ID]component.Component
}

func (h fakeHost) GetExtensions() map[component.ID]component.Component { return h.ext }

type storageClient struct{ *memStore }

func (storageClient) Delete(context.Context, string) error               { return nil }
func (storageClient) Batch(context.Context, ...*storage.Operation) error { return nil }
func (storageClient) Close(context.Context) error                        { return nil }

type storageExt struct {
	component.Component
	client storage.Client
	err    error
}

func (e storageExt) Start(context.Context, component.Host) error { return nil }
func (e storageExt) Shutdown(context.Context) error              { return nil }
func (e storageExt) GetClient(context.Context, component.Kind, component.ID, string) (storage.Client, error) {
	return e.client, e.err
}

type recvFixture struct {
	r       *megaReceiver
	logs    *observer.ObservedLogs
	metrics *consumertest.MetricsSink
	events  *consumertest.LogsSink
	dev     *fakeDevice
}

func newRecvFixture(t *testing.T, cfg *Config, withLogs bool) recvFixture {
	t.Helper()
	core, logs := observer.New(zapcore.DebugLevel)
	set := receivertest.NewNopSettings(component.MustNewType(typeStr))
	set.Logger = zap.New(core)
	r := newReceiver(set, cfg)
	dev := labFake(t)
	r.scraper.find = func() ([]controller, error) { return []controller{testCtrl}, nil }
	r.scraper.open = func(int) (device, func(), error) { return dev, func() {}, nil }
	f := recvFixture{r: r, logs: logs, metrics: new(consumertest.MetricsSink), events: new(consumertest.LogsSink), dev: dev}
	r.metrics = f.metrics
	if withLogs {
		r.logs = f.events
	}
	r.firstDelay = time.Hour
	return f
}

func TestAHostWithoutFileStorageLogsOneErrorAndKeepsSendingMetrics(t *testing.T) {
	cfg := createDefaultConfig().(*Config)
	id := component.MustNewID("file_storage")
	cfg.Events.Storage = &id
	f := newRecvFixture(t, cfg, true)
	require.NoError(t, f.r.Start(context.Background(), fakeHost{Host: componenttest.NewNopHost()}))
	defer func() { require.NoError(t, f.r.Shutdown(context.Background())) }()

	f.r.scrapeAndSend(context.Background())
	f.r.scrapeAndSend(context.Background())

	errs := f.logs.FilterLevelExact(zapcore.ErrorLevel)
	require.Equal(t, 1, errs.Len(), "one error line across two scrapes")
	assert.Contains(t, errs.All()[0].Message, "file_storage")
	assert.Len(t, f.metrics.AllMetrics(), 2, "metrics on both scrapes")
	assert.Zero(t, f.dev.count(opEventGet)+f.dev.count(opEventGetInfo))
}

func TestStorageProblemsAreNamedInTheError(t *testing.T) {
	id := component.MustNewID("file_storage")
	tests := map[string]struct {
		storage *component.ID
		ext     map[component.ID]component.Component
		want    string
	}{
		"no key":         {nil, nil, "events.storage"},
		"not configured": {&id, nil, "is not configured"},
		"client failure": {&id, map[component.ID]component.Component{id: storageExt{err: errors.New("boom")}}, "file_storage client"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			cfg := createDefaultConfig().(*Config)
			cfg.Events.Storage = tt.storage
			f := newRecvFixture(t, cfg, true)
			require.NoError(t, f.r.Start(context.Background(), fakeHost{Host: componenttest.NewNopHost(), ext: tt.ext}))
			defer func() { require.NoError(t, f.r.Shutdown(context.Background())) }()

			f.r.scrapeAndSend(context.Background())

			errs := f.logs.FilterLevelExact(zapcore.ErrorLevel)
			require.Equal(t, 1, errs.Len())
			assert.Contains(t, errs.All()[0].Message, tt.want)
			assert.Contains(t, errs.All()[0].Message, "file_storage")
		})
	}
}

type notStorage struct{ component.Component }

func (notStorage) Start(context.Context, component.Host) error { return nil }
func (notStorage) Shutdown(context.Context) error              { return nil }

func TestAnExtensionThatIsNotAStorageExtensionIsNamed(t *testing.T) {
	id := component.MustNewID("file_storage")
	cfg := createDefaultConfig().(*Config)
	cfg.Events.Storage = &id
	f := newRecvFixture(t, cfg, true)
	host := fakeHost{Host: componenttest.NewNopHost(), ext: map[component.ID]component.Component{id: notStorage{}}}
	require.NoError(t, f.r.Start(context.Background(), host))
	defer func() { require.NoError(t, f.r.Shutdown(context.Background())) }()

	f.r.scrapeAndSend(context.Background())

	errs := f.logs.FilterLevelExact(zapcore.ErrorLevel)
	require.Equal(t, 1, errs.Len())
	assert.Contains(t, errs.All()[0].Message, "not a file_storage extension")
}

func TestAMetricsOnlyPipelineReadsNoEventsAndLogsNoStorageError(t *testing.T) {
	cfg := createDefaultConfig().(*Config)
	f := newRecvFixture(t, cfg, false)
	require.NoError(t, f.r.Start(context.Background(), fakeHost{Host: componenttest.NewNopHost()}))
	defer func() { require.NoError(t, f.r.Shutdown(context.Background())) }()

	f.r.scrapeAndSend(context.Background())

	assert.Zero(t, f.logs.FilterLevelExact(zapcore.ErrorLevel).Len())
	assert.Len(t, f.metrics.AllMetrics(), 1)
}

func TestEventsFlowThroughTheStorageAndWaitForDelivery(t *testing.T) {
	id := component.MustNewID("file_storage")
	cfg := createDefaultConfig().(*Config)
	cfg.Events.Storage = &id
	store := newMemStore()
	store.data[cursorKey(testCtrl)] = []byte("1")
	f := newRecvFixture(t, cfg, true)
	eventLog(f.dev, 3, allInfo)
	host := fakeHost{Host: componenttest.NewNopHost(), ext: map[component.ID]component.Component{id: storageExt{client: storageClient{store}}}}
	require.NoError(t, f.r.Start(context.Background(), host))
	defer func() { require.NoError(t, f.r.Shutdown(context.Background())) }()

	f.r.logs = consumertest.NewErr(errors.New("downstream is full"))
	f.r.scrapeAndSend(context.Background())
	assert.Equal(t, uint32(1), store.seq(testCtrl), "a failed delivery leaves the position, so the events are read again")

	f.r.logs = f.events
	f.r.scrapeAndSend(context.Background())
	assert.Equal(t, uint32(3), store.seq(testCtrl))
	require.Len(t, f.events.AllLogs(), 1)
	assert.Equal(t, 2, f.events.LogRecordCount())
	assert.Equal(t, 1, f.logs.FilterMessage("Failed to send megaraid events; they are read again").Len())
}

func TestAFailureToSaveThePositionIsLoggedNotFatal(t *testing.T) {
	id := component.MustNewID("file_storage")
	cfg := createDefaultConfig().(*Config)
	cfg.Events.Storage = &id
	store := newMemStore()
	store.data[cursorKey(testCtrl)] = []byte("1")
	store.failSet = errors.New("disk full")
	f := newRecvFixture(t, cfg, true)
	eventLog(f.dev, 3, allInfo)
	host := fakeHost{Host: componenttest.NewNopHost(), ext: map[component.ID]component.Component{id: storageExt{client: storageClient{store}}}}
	require.NoError(t, f.r.Start(context.Background(), host))
	defer func() { require.NoError(t, f.r.Shutdown(context.Background())) }()

	f.r.scrapeAndSend(context.Background())

	assert.Equal(t, 1, f.logs.FilterMessage("Failed to save the event position").Len())
}

func TestTheLoopScrapesOnStartAndStopsOnShutdown(t *testing.T) {
	cfg := createDefaultConfig().(*Config)
	cfg.Events.Enabled = false
	cfg.CollectionInterval = 20 * time.Millisecond
	f := newRecvFixture(t, cfg, false)
	f.r.firstDelay = 0

	require.NoError(t, f.r.Start(context.Background(), componenttest.NewNopHost()))
	require.Eventually(t, func() bool { return len(f.metrics.AllMetrics()) >= 2 }, 5*time.Second, 10*time.Millisecond)
	require.NoError(t, f.r.Shutdown(context.Background()))

	n := len(f.metrics.AllMetrics())
	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, n, len(f.metrics.AllMetrics()), "no scrape after shutdown")
}

func TestAMetricsConsumerErrorIsLogged(t *testing.T) {
	cfg := createDefaultConfig().(*Config)
	cfg.Events.Enabled = false
	f := newRecvFixture(t, cfg, false)
	f.r.metrics = consumertest.NewErr(errors.New("no"))

	f.r.scrapeAndSend(context.Background())

	assert.Equal(t, 1, f.logs.FilterMessage("Failed to send megaraid metrics").Len())
}

func TestStartSendsNothingToAnyController(t *testing.T) {
	cfg := createDefaultConfig().(*Config)
	cfg.Events.Enabled = false
	f := newRecvFixture(t, cfg, false)

	require.NoError(t, f.r.Start(context.Background(), componenttest.NewNopHost()))
	defer func() { require.NoError(t, f.r.Shutdown(context.Background())) }()

	assert.Empty(t, f.dev.sent)
}
