// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package ksmreceiver

import (
	"context"
	"errors"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component/componenttest"
	"go.opentelemetry.io/collector/confmap"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/receiver/receivertest"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

var sevenFiles = []string{
	"pages_shared", "pages_sharing", "pages_unshared", "pages_volatile", "full_scans",
	"stable_node_chains", "stable_node_dups",
}

func fixtureConfig() *Config {
	cfg := createDefaultConfig().(*Config)
	cfg.KsmPath = filepath.Join("testdata", "ksm")
	return cfg
}

func newTestScraper(cfg *Config) (*scraper, *observer.ObservedLogs) {
	core, logs := observer.New(zapcore.DebugLevel)
	return newScraper(zap.New(core), cfg), logs
}

func scrapeOnce(t *testing.T, s *scraper) pmetric.Metrics {
	t.Helper()
	md, err := s.scrape(context.Background())
	require.NoError(t, err)
	return md
}

func metricsByName(md pmetric.Metrics) map[string]pmetric.Metric {
	out := map[string]pmetric.Metric{}
	for i := 0; i < md.ResourceMetrics().Len(); i++ {
		sms := md.ResourceMetrics().At(i).ScopeMetrics()
		for j := 0; j < sms.Len(); j++ {
			for k := 0; k < sms.At(j).Metrics().Len(); k++ {
				m := sms.At(j).Metrics().At(k)
				out[m.Name()] = m
			}
		}
	}
	return out
}

func onlyPoint(t *testing.T, m pmetric.Metric) pmetric.NumberDataPoint {
	t.Helper()
	var dps pmetric.NumberDataPointSlice
	if m.Type() == pmetric.MetricTypeGauge {
		dps = m.Gauge().DataPoints()
	} else {
		dps = m.Sum().DataPoints()
	}
	require.Equal(t, 1, dps.Len())
	return dps.At(0)
}

func warned(logs *observer.ObservedLogs) []observer.LoggedEntry {
	return logs.FilterLevelExact(zapcore.WarnLevel).All()
}

func writeKsm(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644))
	}
	return dir
}

func TestTheSevenFilesGiveTheSevenSeriesWithTheirNamesUnitsAndValues(t *testing.T) {
	s, _ := newTestScraper(fixtureConfig())

	got := metricsByName(scrapeOnce(t, s))

	want := map[string]struct {
		unit  string
		value int64
		typ   pmetric.MetricType
	}{
		"linux.ksm.stats.pages_shared":       {"{page}", 1234, pmetric.MetricTypeGauge},
		"linux.ksm.stats.pages_sharing":      {"{page}", 56789, pmetric.MetricTypeGauge},
		"linux.ksm.stats.pages_unshared":     {"{page}", 222222, pmetric.MetricTypeGauge},
		"linux.ksm.stats.pages_volatile":     {"{page}", 333, pmetric.MetricTypeGauge},
		"linux.ksm.stats.full_scans":         {"{scan}", 42, pmetric.MetricTypeSum},
		"linux.ksm.stats.stable_node_chains": {"{node}", 3, pmetric.MetricTypeGauge},
		"linux.ksm.stats.stable_node_dups":   {"{node}", 17, pmetric.MetricTypeGauge},
	}
	require.Len(t, got, len(want))
	for name, w := range want {
		m, ok := got[name]
		require.True(t, ok, name)
		assert.Equal(t, w.unit, m.Unit(), name)
		assert.Equal(t, w.value, onlyPoint(t, m).IntValue(), name)
		assert.Equal(t, w.typ, m.Type(), name)
	}
	sum := got["linux.ksm.stats.full_scans"].Sum()
	assert.True(t, sum.IsMonotonic())
	assert.Equal(t, pmetric.AggregationTemporalityCumulative, sum.AggregationTemporality())
}

func TestATreeWithoutTheTwoNodeFilesGivesFiveSeriesAndNoWarning(t *testing.T) {
	dir := writeKsm(t, map[string]string{
		"pages_shared": "1\n", "pages_sharing": "2\n", "pages_unshared": "3\n", "pages_volatile": "4\n", "full_scans": "5\n",
	})
	cfg := fixtureConfig()
	cfg.KsmPath = dir
	s, logs := newTestScraper(cfg)

	got := metricsByName(scrapeOnce(t, s))

	assert.Len(t, got, 5)
	assert.NotContains(t, got, "linux.ksm.stats.stable_node_chains")
	assert.Empty(t, warned(logs))
}

func TestAMissingPathGivesNothingAndOneInfoLineForTheLifeOfTheScraper(t *testing.T) {
	cfg := fixtureConfig()
	cfg.KsmPath = filepath.Join(t.TempDir(), "absent")
	s, logs := newTestScraper(cfg)

	first := scrapeOnce(t, s)
	scrapeOnce(t, s)

	assert.Equal(t, 0, first.ResourceMetrics().Len())
	assert.Equal(t, 1, logs.FilterLevelExact(zapcore.InfoLevel).FilterMessageSnippet("not found").Len())
	assert.Empty(t, warned(logs))
}

func TestAPathThatAppearsLaterIsReadAndTheInfoLineComesBackIfItGoesAway(t *testing.T) {
	root := t.TempDir()
	cfg := fixtureConfig()
	cfg.KsmPath = filepath.Join(root, "ksm")
	s, logs := newTestScraper(cfg)
	scrapeOnce(t, s)
	require.NoError(t, os.Mkdir(cfg.KsmPath, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(cfg.KsmPath, "pages_shared"), []byte("9\n"), 0o644))

	found := scrapeOnce(t, s)
	require.NoError(t, os.RemoveAll(cfg.KsmPath))
	scrapeOnce(t, s)

	assert.Contains(t, metricsByName(found), "linux.ksm.stats.pages_shared")
	assert.Equal(t, 2, logs.FilterLevelExact(zapcore.InfoLevel).FilterMessageSnippet("not found").Len())
}

func TestAnUnreadableFileIsSkippedWithOneWarningPerEpisode(t *testing.T) {
	cfg := fixtureConfig()
	s, logs := newTestScraper(cfg)
	src := s.src.(*fileSource)
	broken := true
	orig := src.open
	// The unexported open hook is the only way to simulate a read error without root.
	src.open = func(path string) ([]byte, error) {
		if broken && filepath.Base(path) == "pages_sharing" {
			return nil, syscall.EACCES
		}
		return orig(path)
	}

	first := metricsByName(scrapeOnce(t, s))
	scrapeOnce(t, s)
	broken = false
	healed := metricsByName(scrapeOnce(t, s))
	broken = true
	scrapeOnce(t, s)

	assert.NotContains(t, first, "linux.ksm.stats.pages_sharing")
	assert.Contains(t, first, "linux.ksm.stats.pages_shared", "the other files are read")
	assert.Contains(t, healed, "linux.ksm.stats.pages_sharing")
	require.Len(t, warned(logs), 2, "one warning in each of the two episodes")
	assert.Equal(t, "pages_sharing", warned(logs)[0].ContextMap()["file"])
}

func TestAFileThatIsNotANumberIsSkippedWithOneWarning(t *testing.T) {
	dir := writeKsm(t, map[string]string{"pages_shared": "many\n", "pages_sharing": "7\n", "full_scans": "-3\n", "pages_volatile": ""})
	cfg := fixtureConfig()
	cfg.KsmPath = dir
	s, logs := newTestScraper(cfg)

	got := metricsByName(scrapeOnce(t, s))
	scrapeOnce(t, s)

	assert.Equal(t, []string{"linux.ksm.stats.pages_sharing"}, sortedNames(got))
	assert.Len(t, warned(logs), 3, "pages_shared, full_scans and pages_volatile, once each")
}

func sortedNames(m map[string]pmetric.Metric) []string {
	out := make([]string, 0, len(m))
	for name := range m {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func TestAValueAboveInt64IsClamped(t *testing.T) {
	dir := writeKsm(t, map[string]string{"pages_shared": "18446744073709551615\n"})
	cfg := fixtureConfig()
	cfg.KsmPath = dir
	s, _ := newTestScraper(cfg)

	got := metricsByName(scrapeOnce(t, s))

	assert.Equal(t, int64(1<<63-1), onlyPoint(t, got["linux.ksm.stats.pages_shared"]).IntValue())
}

func TestFullScansRestartsItsSumWhenTheCounterGoesDown(t *testing.T) {
	dir := writeKsm(t, map[string]string{"full_scans": "10\n"})
	cfg := fixtureConfig()
	cfg.KsmPath = dir
	s, _ := newTestScraper(cfg)
	startOf := func() pcommon.Timestamp {
		return onlyPoint(t, metricsByName(scrapeOnce(t, s))["linux.ksm.stats.full_scans"]).StartTimestamp()
	}

	first := startOf()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "full_scans"), []byte("12\n"), 0o644))
	second := startOf()
	waitUntilAfter(t, second)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "full_scans"), []byte("1\n"), 0o644))
	third := startOf()

	assert.Equal(t, first, second)
	assert.Greater(t, third, second, "the sum restarts at the scrape that saw it drop")
}

func TestOnlyTheSevenFilesAreOpenedAndEveryOtherPathFails(t *testing.T) {
	s, _ := newTestScraper(fixtureConfig())
	src := s.src.(*fileSource)
	orig := src.open
	var opened []string
	// The unexported open hook is the only way to see which paths the scraper opens.
	src.open = func(path string) ([]byte, error) {
		name := filepath.Base(path)
		opened = append(opened, name)
		if !contains(sevenFiles, name) {
			t.Errorf("opened %s, which is not one of the seven files", name)
			return nil, errors.New("not allowed")
		}
		return orig(path)
	}

	scrapeOnce(t, s)

	sort.Strings(opened)
	want := append([]string(nil), sevenFiles...)
	sort.Strings(want)
	assert.Equal(t, want, opened)
}

// waitUntilAfter blocks until the clock is past ts, so a later scrape gets a later timestamp.
func waitUntilAfter(t *testing.T, ts pcommon.Timestamp) {
	t.Helper()
	require.Eventually(t, func() bool { return time.Now().After(ts.AsTime()) }, time.Second, time.Millisecond)
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func TestNoSourceFileImportsOsExec(t *testing.T) {
	entries, err := os.ReadDir(".")
	require.NoError(t, err)
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), e.Name(), nil, parser.ImportsOnly)
		require.NoError(t, err)
		for _, imp := range f.Imports {
			assert.NotEqual(t, `"os/exec"`, imp.Path.Value, e.Name())
		}
	}
}

func TestDefaultsAndKeys(t *testing.T) {
	cfg := createDefaultConfig().(*Config)

	assert.Equal(t, 60*time.Second, cfg.CollectionInterval)
	assert.Equal(t, "/sys/kernel/mm/ksm", cfg.KsmPath)
	assert.NoError(t, cfg.Validate())

	cm := confmap.NewFromStringMap(map[string]any{"collection_interval": "30s", "ksm_path": "/x/ksm"})
	require.NoError(t, cm.Unmarshal(cfg))
	assert.Equal(t, 30*time.Second, cfg.CollectionInterval)
	assert.Equal(t, "/x/ksm", cfg.KsmPath)
}

func TestValidateRejectsABadIntervalOrAnEmptyPath(t *testing.T) {
	for name, mutate := range map[string]func(*Config){
		"zero interval":     func(c *Config) { c.CollectionInterval = 0 },
		"negative interval": func(c *Config) { c.CollectionInterval = -time.Second },
		"under a second":    func(c *Config) { c.CollectionInterval = 500 * time.Millisecond },
		"empty path":        func(c *Config) { c.KsmPath = "" },
	} {
		cfg := createDefaultConfig().(*Config)
		mutate(cfg)

		assert.Error(t, cfg.Validate(), name)
	}
}

func TestFactoryTypeAndMetricsOnly(t *testing.T) {
	f := NewFactory()
	set := receivertest.NewNopSettings(f.Type())

	r, err := f.CreateMetrics(context.Background(), set, f.CreateDefaultConfig(), consumertest.NewNop())
	require.NoError(t, err)
	assert.NotNil(t, r)
	assert.Equal(t, "ksm", f.Type().String())
	_, err = f.CreateLogs(context.Background(), set, f.CreateDefaultConfig(), consumertest.NewNop())
	assert.Error(t, err)
}

func buildReceiver(t *testing.T, cfg *Config, sink *consumertest.MetricsSink) *ksmReceiver {
	t.Helper()
	f := NewFactory()
	r, err := f.CreateMetrics(context.Background(), receivertest.NewNopSettings(f.Type()), cfg, sink)
	require.NoError(t, err)
	return r.(*ksmReceiver)
}

func TestTheReceiverSendsOnStartAndEachIntervalAndStopsCleanly(t *testing.T) {
	sink := new(consumertest.MetricsSink)
	cfg := fixtureConfig()
	cfg.CollectionInterval = time.Second
	r := buildReceiver(t, cfg, sink)

	require.NoError(t, r.Start(context.Background(), componenttest.NewNopHost()))
	require.Eventually(t, func() bool { return len(sink.AllMetrics()) >= 2 }, 4*time.Second, 50*time.Millisecond)
	require.NoError(t, r.Shutdown(context.Background()))

	assert.Contains(t, metricsByName(sink.AllMetrics()[0]), "linux.ksm.stats.pages_shared")
}

func TestTheReceiverSendsNothingWithoutKsm(t *testing.T) {
	sink := new(consumertest.MetricsSink)
	cfg := fixtureConfig()
	cfg.KsmPath = filepath.Join(t.TempDir(), "none")
	r := buildReceiver(t, cfg, sink)
	core, logs := observer.New(zapcore.DebugLevel)
	r.scraper = newScraper(zap.New(core), cfg)
	require.NoError(t, r.Start(context.Background(), componenttest.NewNopHost()))

	require.Eventually(t, func() bool { return logs.FilterMessageSnippet("not found").Len() == 1 }, 5*time.Second, time.Millisecond)
	require.NoError(t, r.Shutdown(context.Background()))

	assert.Empty(t, sink.AllMetrics())
}

// refusingConsumer reports each batch on called and answers with an error.
type refusingConsumer struct{ called chan struct{} }

func (c refusingConsumer) ConsumeMetrics(context.Context, pmetric.Metrics) error {
	select {
	case c.called <- struct{}{}:
	default:
	}
	return errors.New("downstream refused")
}

func (refusingConsumer) Capabilities() consumer.Capabilities { return consumer.Capabilities{} }

func TestTheReceiverSurvivesAConsumerError(t *testing.T) {
	failing := buildReceiver(t, fixtureConfig(), nil)
	called := make(chan struct{}, 1)
	failing.consumer = refusingConsumer{called: called}
	require.NoError(t, failing.Start(context.Background(), componenttest.NewNopHost()))

	select {
	case <-called:
	case <-time.After(5 * time.Second):
		t.Fatal("the consumer was never called")
	}

	assert.NoError(t, failing.Shutdown(context.Background()))
}

func TestShutdownBeforeStartIsSafe(t *testing.T) {
	r := buildReceiver(t, fixtureConfig(), new(consumertest.MetricsSink))

	assert.NoError(t, r.Shutdown(context.Background()))
}

func TestACanceledContextStopsTheScrape(t *testing.T) {
	s, _ := newTestScraper(fixtureConfig())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := s.scrape(ctx)

	assert.Error(t, err)
}

func TestDocsListTheKeysTheMetricsAndTheSampleAndTheChangelogHasAGrowthRow(t *testing.T) {
	doc, err := os.ReadFile(filepath.Join("..", "..", "docs", "otel-metrics.md"))
	require.NoError(t, err)
	_, rest, ok := strings.Cut(string(doc), "### KSM")
	require.True(t, ok, "the KSM section is missing")
	section, _, _ := strings.Cut(rest, "\n### ")
	for _, want := range []string{
		"collection_interval", "ksm_path", "/sys/kernel/mm/ksm", "linux.ksm.stats.pages_shared", "linux.ksm.stats.pages_sharing",
		"linux.ksm.stats.pages_unshared", "linux.ksm.stats.pages_volatile", "linux.ksm.stats.full_scans",
		"linux.ksm.stats.stable_node_chains", "linux.ksm.stats.stable_node_dups", "{page}", "{scan}", "{node}",
	} {
		assert.Contains(t, section, want)
	}
	changelog, err := os.ReadFile(filepath.Join("..", "..", "CHANGELOG.md"))
	require.NoError(t, err)
	assert.Contains(t, string(changelog), "`ksm` receiver")
	assert.Contains(t, string(changelog), "Binary size")
	sample, err := os.ReadFile(filepath.Join("..", "..", "test", "configs", "edge-ksm.yaml"))
	require.NoError(t, err)
	assert.Contains(t, string(sample), "ksm:")
}
