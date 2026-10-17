// Modified by Vlad Korolev, 2026-10: lifecycle tests against a recording port; metric-content tests move to the collect package.
// Original: https://github.com/pmady/otel-gpu-receiver@ac756935ca040923d98af5a0d0547dca02a3b23b/scraper_test.go

package gpureceiver

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/vladistan/otelcol-custom/receiver/gpureceiver/internal/port"
	"github.com/vladistan/otelcol-custom/receiver/gpureceiver/internal/semconv"
)

// recordingPort counts calls and returns scripted results.
type recordingPort struct {
	mu         sync.Mutex
	names      []string
	initErr    error
	version    string
	versionErr error
	countErr   error
	devices    []port.Device
	deviceErrs map[int]error
}

func (p *recordingPort) record(name string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.names = append(p.names, name)
}

func (p *recordingPort) calls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.names)
}

func (p *recordingPort) count(name string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	for _, got := range p.names {
		if got == name {
			n++
		}
	}
	return n
}

func (p *recordingPort) Init() error {
	p.record("Init")
	return p.initErr
}

func (p *recordingPort) Shutdown() { p.record("Shutdown") }

func (p *recordingPort) DriverVersion() (string, error) {
	p.record("DriverVersion")
	return p.version, p.versionErr
}

func (p *recordingPort) DeviceCount() (int, error) {
	p.record("DeviceCount")
	return len(p.devices), p.countErr
}

func (p *recordingPort) Device(index int) (port.Device, error) {
	p.record("Device")
	if err := p.deviceErrs[index]; err != nil {
		return nil, err
	}
	return p.devices[index], nil
}

// stubDevice answers identity calls only; the embedded nil Device panics on
// any other call, which the lifecycle must never make.
type stubDevice struct {
	port.Device
	uuid, name       string
	uuidErr, nameErr error
}

func (d *stubDevice) UUID() (string, error) { return d.uuid, d.uuidErr }
func (d *stubDevice) Name() (string, error) { return d.name, d.nameErr }

type buildArgs struct {
	devices []port.DeviceIdentity
	toggles port.Toggles
	start   time.Time
}

type stubCollector struct{ at []time.Time }

func (c *stubCollector) Collect(now time.Time) pmetric.Metrics {
	c.at = append(c.at, now)
	md := pmetric.NewMetrics()
	md.ResourceMetrics().AppendEmpty()
	return md
}

type scraperFixture struct {
	scraper   *gpuScraper
	port      *recordingPort
	logs      *observer.ObservedLogs
	collector *stubCollector
	built     []buildArgs
	clock     time.Time
}

func newFixture(p *recordingPort) *scraperFixture {
	core, logs := observer.New(zapcore.DebugLevel)
	f := &scraperFixture{
		port:      p,
		logs:      logs,
		collector: &stubCollector{},
		clock:     time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC),
	}
	build := func(
		_ port.Port, devices []port.DeviceIdentity, toggles port.Toggles, start time.Time, _ *zap.Logger,
	) port.Collector {
		f.built = append(f.built, buildArgs{devices: devices, toggles: toggles, start: start})
		return f.collector
	}
	f.scraper = newGPUScraper(zap.New(core), createDefaultConfig(), p, build)
	f.scraper.now = func() time.Time { return f.clock }
	return f
}

func (f *scraperFixture) warnings() []string {
	var out []string
	for _, entry := range f.logs.FilterLevelExact(zapcore.WarnLevel).All() {
		out = append(out, entry.Message)
	}
	return out
}

func twoGPUs() []port.Device {
	return []port.Device{
		&stubDevice{uuid: "GPU-0", name: "A100"},
		&stubDevice{uuid: "GPU-1", name: "A100"},
	}
}

// AC-023a: the first scrape makes exactly one Init call, later scrapes none.
func TestFirstScrapeInitialisesOnce(t *testing.T) {
	f := newFixture(&recordingPort{version: "550.54", devices: twoGPUs()})
	require.NoError(t, f.scraper.start(context.Background(), nil))
	assert.Zero(t, f.port.calls())

	for range 3 {
		_, err := f.scraper.scrape(context.Background())
		require.NoError(t, err)
	}

	assert.Equal(t, 1, f.port.count("Init"))
	assert.Equal(t, 1, f.port.count("DeviceCount"))
	assert.Len(t, f.built, 1)
	assert.Len(t, f.collector.at, 3)
}

func scrapeOnce(t *testing.T, f *scraperFixture) pmetric.Metrics {
	t.Helper()
	md, err := f.scraper.scrape(context.Background())
	require.NoError(t, err)
	require.Len(t, f.built, 1)
	return md
}

func TestScrapeReturnsCollectorBatch(t *testing.T) {
	f := newFixture(&recordingPort{devices: twoGPUs()})

	md := scrapeOnce(t, f)

	assert.Equal(t, 1, md.ResourceMetrics().Len())
}

func TestScrapePassesStartTimeToBuilder(t *testing.T) {
	f := newFixture(&recordingPort{devices: twoGPUs()})
	require.NoError(t, f.scraper.start(context.Background(), nil))
	started := f.clock
	f.clock = f.clock.Add(10 * time.Second)

	scrapeOnce(t, f)

	assert.Equal(t, started, f.built[0].start)
}

func TestScrapePassesConfigTogglesToBuilder(t *testing.T) {
	f := newFixture(&recordingPort{devices: twoGPUs()})
	f.scraper.config.Metrics[semconv.MetricClock] = MetricConfig{Enabled: false}

	scrapeOnce(t, f)

	assert.False(t, f.built[0].toggles[semconv.MetricClock])
	assert.True(t, f.built[0].toggles[semconv.MetricIO])
}

func TestScrapeCollectsAtScrapeTime(t *testing.T) {
	f := newFixture(&recordingPort{devices: twoGPUs()})
	f.clock = f.clock.Add(10 * time.Second)

	scrapeOnce(t, f)

	assert.Equal(t, []time.Time{f.clock}, f.collector.at)
}

func TestScrapePassesDeviceIdentitiesToBuilder(t *testing.T) {
	f := newFixture(&recordingPort{version: "550.54", devices: twoGPUs()})

	scrapeOnce(t, f)

	assert.Equal(t, []port.DeviceIdentity{
		{Index: 0, UUID: "GPU-0", Model: "A100", Name: "gpu0", DriverVersion: "550.54"},
		{Index: 1, UUID: "GPU-1", Model: "A100", Name: "gpu1", DriverVersion: "550.54"},
	}, f.built[0].devices)
}

// EC-001..005: every Init failure kind gives one WARN, then silent empty scrapes.
func TestInitFailureWarnsOnceThenStaysSilent(t *testing.T) {
	for _, kind := range []port.Kind{
		port.LibraryNotFound, port.DriverNotLoaded, port.NoPermission, port.VersionMismatch, port.Other,
	} {
		t.Run(kind.String(), func(t *testing.T) {
			f := newFixture(&recordingPort{initErr: port.NewError(kind, errors.New("boom"))})

			for range 3 {
				md, err := f.scraper.scrape(context.Background())
				require.NoError(t, err)
				assert.Zero(t, md.ResourceMetrics().Len())
			}

			require.Len(t, f.warnings(), 1)
			entry := f.logs.FilterLevelExact(zapcore.WarnLevel).All()[0]
			assert.Equal(t, kind.String(), entry.ContextMap()["reason"])
			assert.Equal(t, 1, f.port.count("Init"))
			assert.Empty(t, f.built)
		})
	}
}

func TestZeroDevicesWarnsOnce(t *testing.T) {
	f := newFixture(&recordingPort{})

	for range 2 {
		md, err := f.scraper.scrape(context.Background())
		require.NoError(t, err)
		assert.Zero(t, md.ResourceMetrics().Len())
	}

	assert.Equal(t, []string{"No NVIDIA GPU devices found"}, f.warnings())
	assert.Empty(t, f.built)
}

func TestDeviceCountFailureWarnsOnceAndDisables(t *testing.T) {
	f := newFixture(&recordingPort{countErr: port.NewError(port.Other, errors.New("boom"))})

	for range 2 {
		_, err := f.scraper.scrape(context.Background())
		require.NoError(t, err)
	}

	assert.Len(t, f.warnings(), 1)
	assert.Empty(t, f.built)
}

// A failing index is excluded with one WARN; the others are kept.
func TestEnumerationFailureExcludesIndex(t *testing.T) {
	devices := []port.Device{
		&stubDevice{uuid: "GPU-0", name: "A100"},
		&stubDevice{uuid: "GPU-1", name: "A100"},
		&stubDevice{uuid: "GPU-2", name: "A100", nameErr: port.NewError(port.Other, nil)},
		&stubDevice{uuid: "GPU-3", name: "A100", uuidErr: port.NewError(port.Other, nil)},
	}
	f := newFixture(&recordingPort{
		devices:    devices,
		deviceErrs: map[int]error{1: port.NewError(port.Other, errors.New("handle"))},
	})

	_, err := f.scraper.scrape(context.Background())

	require.NoError(t, err)
	require.Len(t, f.built, 1)
	require.Len(t, f.built[0].devices, 1)
	assert.Equal(t, "GPU-0", f.built[0].devices[0].UUID)
	assert.Len(t, f.warnings(), 3)
}

func TestAllDevicesExcludedDisablesCollector(t *testing.T) {
	f := newFixture(&recordingPort{
		devices:    []port.Device{nil},
		deviceErrs: map[int]error{0: port.NewError(port.Other, nil)},
	})

	md, err := f.scraper.scrape(context.Background())

	require.NoError(t, err)
	assert.Zero(t, md.ResourceMetrics().Len())
	assert.Empty(t, f.built)
	assert.Equal(t, []string{"GPU excluded: enumeration failed", "No usable NVIDIA GPU devices"}, f.warnings())
}

func TestStartupInfoLogsDeviceCountAndDriverVersion(t *testing.T) {
	f := newFixture(&recordingPort{version: "550.54", devices: twoGPUs()})

	_, err := f.scraper.scrape(context.Background())

	require.NoError(t, err)
	infos := f.logs.FilterLevelExact(zapcore.InfoLevel).All()
	require.Len(t, infos, 1)
	assert.Equal(t, int64(2), infos[0].ContextMap()["devices"])
	assert.Equal(t, "550.54", infos[0].ContextMap()["driver_version"])
}

func TestShutdownClosesNVMLOnlyAfterSuccessfulInit(t *testing.T) {
	t.Run("never scraped", func(t *testing.T) {
		f := newFixture(&recordingPort{})
		require.NoError(t, f.scraper.shutdown(context.Background()))
		assert.Zero(t, f.port.calls())
	})
	t.Run("init failed", func(t *testing.T) {
		f := newFixture(&recordingPort{initErr: port.NewError(port.DriverNotLoaded, nil)})
		_, err := f.scraper.scrape(context.Background())
		require.NoError(t, err)
		require.NoError(t, f.scraper.shutdown(context.Background()))
		assert.Zero(t, f.port.count("Shutdown"))
	})
	t.Run("init succeeded", func(t *testing.T) {
		f := newFixture(&recordingPort{devices: twoGPUs()})
		_, err := f.scraper.scrape(context.Background())
		require.NoError(t, err)
		require.NoError(t, f.scraper.shutdown(context.Background()))
		require.NoError(t, f.scraper.shutdown(context.Background()))
		assert.Equal(t, 1, f.port.count("Shutdown"))
	})
}

func TestDriverVersionFailureDoesNotDisable(t *testing.T) {
	f := newFixture(&recordingPort{
		versionErr: port.NewError(port.Other, nil),
		devices:    twoGPUs(),
	})

	_, err := f.scraper.scrape(context.Background())

	require.NoError(t, err)
	require.Len(t, f.built, 1)
	assert.Empty(t, f.built[0].devices[0].DriverVersion)
	assert.Empty(t, f.warnings())
}
