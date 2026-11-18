// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package ksmreceiver

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component/componenttest"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func newObserved() (zapcore.Core, *observer.ObservedLogs) { return observer.New(zapcore.DebugLevel) }

func TestTheScrapeLoopKeepsRunningAfterTheStartContextIsCanceled(t *testing.T) {
	sink := new(consumertest.MetricsSink)
	cfg := fixtureConfig()
	cfg.CollectionInterval = 40 * time.Millisecond
	r := buildReceiver(t, cfg, sink)
	ctx, cancel := context.WithCancel(context.Background())

	require.NoError(t, r.Start(ctx, componenttest.NewNopHost()))
	cancel()
	seen := len(sink.AllMetrics())

	assert.Eventually(t, func() bool { return len(sink.AllMetrics()) >= seen+3 }, 3*time.Second, 20*time.Millisecond)
	require.NoError(t, r.Shutdown(context.Background()))
}

// linuxEdgeManifests are the builder files of the Linux edge and the static Linux edge.
func linuxEdgeManifests(t *testing.T) (all, edge []string) {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join("..", "..", "builder-*.yaml"))
	require.NoError(t, err)
	for _, p := range paths {
		name := filepath.Base(p)
		all = append(all, name)
		if strings.HasPrefix(name, "builder-edge") && !strings.Contains(name, "freebsd") && !strings.Contains(name, "windows") {
			edge = append(edge, name)
		}
	}
	sort.Strings(all)
	sort.Strings(edge)
	return all, edge
}

// receiversListing returns the gomod and replace lines of a manifest that are not comments, with the
// section each one is in.
func receiversListing(t *testing.T, name string) (inReceivers, inReplaces bool) {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "..", name))
	require.NoError(t, err)
	section := ""
	for _, line := range strings.Split(string(body), "\n") {
		if line != "" && line[0] != ' ' && line[0] != '#' && line[0] != '-' {
			section = strings.TrimSuffix(strings.TrimSpace(line), ":")
			continue
		}
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") || !strings.Contains(trimmed, "receiver/ksmreceiver") {
			continue
		}
		switch {
		case section == "receivers" && strings.HasPrefix(trimmed, "- gomod:"):
			inReceivers = true
		case section == "replaces" && strings.HasPrefix(trimmed, "- github.com/"):
			inReplaces = true
		}
	}
	return inReceivers, inReplaces
}

func TestOnlyTheLinuxEdgeManifestsHaveAKsmGomodLineInTheReceiversSection(t *testing.T) {
	all, edge := linuxEdgeManifests(t)
	require.Equal(t, []string{"builder-edge-static.yaml", "builder-edge.yaml"}, edge, "the Linux edge manifests found by glob")

	for _, name := range all {
		wantListed := contains(edge, name)
		receivers, replaces := receiversListing(t, name)

		assert.Equal(t, wantListed, receivers, "%s: gomod line in receivers", name)
		assert.Equal(t, wantListed, replaces, "%s: replace line", name)
	}
}

func TestAKsmPathThatIsAFileGivesOneWarningAndNoSeries(t *testing.T) {
	file := filepath.Join(t.TempDir(), "ksm")
	require.NoError(t, os.WriteFile(file, []byte("1\n"), 0o644))
	cfg := fixtureConfig()
	cfg.KsmPath = file
	s, logs := newTestScraper(cfg)

	first := scrapeOnce(t, s)
	scrapeOnce(t, s)

	assert.Equal(t, 0, first.ResourceMetrics().Len())
	require.Len(t, warned(logs), 1, "one warning, not one for each file")
	assert.Contains(t, warned(logs)[0].Message, "not a directory")
	assert.Zero(t, logs.FilterLevelExact(zapcore.InfoLevel).FilterMessageSnippet("not found").Len())
}

func TestAnUnreadableParentIsNotReportedAsAMissingDirectory(t *testing.T) {
	s, logs := newTestScraper(fixtureConfig())
	src := s.src.(*fileSource)
	broken := true
	orig := src.stat
	// The unexported stat hook is the only way to simulate an access error without root.
	src.stat = func(path string) (fs.FileInfo, error) {
		if broken {
			return nil, &fs.PathError{Op: "stat", Path: path, Err: syscall.EACCES}
		}
		return orig(path)
	}

	denied := scrapeOnce(t, s)
	scrapeOnce(t, s)
	broken = false
	healed := scrapeOnce(t, s)

	assert.Equal(t, 0, denied.ResourceMetrics().Len())
	assert.Zero(t, logs.FilterLevelExact(zapcore.InfoLevel).FilterMessageSnippet("not found").Len())
	require.Len(t, warned(logs), 1, "one warning for the episode")
	assert.Contains(t, warned(logs)[0].ContextMap()["error"], "permission denied")
	assert.Contains(t, metricsByName(healed), "linux.ksm.stats.pages_shared")
}

// startOfFullScans scrapes and returns the start time of the full_scans sum, or 0 when it is absent.
func startOfFullScans(t *testing.T, s *scraper) pcommon.Timestamp {
	t.Helper()
	m, ok := metricsByName(scrapeOnce(t, s))["linux.ksm.stats.full_scans"]
	if !ok {
		return 0
	}
	return onlyPoint(t, m).StartTimestamp()
}

func TestFullScansRestartsWhenTheFileWasUnreadableBetweenTwoScrapes(t *testing.T) {
	dir := writeKsm(t, map[string]string{"full_scans": "10\n"})
	cfg := fixtureConfig()
	cfg.KsmPath = dir
	s, _ := newTestScraper(cfg)
	first := startOfFullScans(t, s)
	require.NoError(t, os.Remove(filepath.Join(dir, "full_scans")))
	require.Zero(t, startOfFullScans(t, s))
	waitUntilAfter(t, first)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "full_scans"), []byte("11\n"), 0o644))

	back := startOfFullScans(t, s)

	assert.Greater(t, back, first, "a restart that climbed above the old value is not missed")
	assert.Equal(t, back, startOfFullScans(t, s), "the start stays put while reads are clean")
}

func TestFullScansRestartsWhenTheDirectoryWentAwayBetweenTwoScrapes(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "ksm")
	require.NoError(t, os.Mkdir(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "full_scans"), []byte("5\n"), 0o644))
	cfg := fixtureConfig()
	cfg.KsmPath = dir
	s, _ := newTestScraper(cfg)
	first := startOfFullScans(t, s)
	require.NoError(t, os.RemoveAll(dir))
	scrapeOnce(t, s)
	waitUntilAfter(t, first)
	require.NoError(t, os.Mkdir(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "full_scans"), []byte("9\n"), 0o644))

	assert.Greater(t, startOfFullScans(t, s), first)
}

func TestWhenNoFileCanBeReadTheWarningComesBackAtALowRate(t *testing.T) {
	s, logs := newTestScraper(fixtureConfig())
	// The unexported open hook is the only way to simulate a read error without root.
	s.src.(*fileSource).open = func(string) ([]byte, error) { return nil, syscall.EACCES }

	for range 2 * unreadableEvery {
		assert.Equal(t, 0, scrapeOnce(t, s).ResourceMetrics().Len())
	}

	all := logs.FilterLevelExact(zapcore.WarnLevel).FilterMessageSnippet("None of the KSM files").Len()
	assert.Equal(t, 2, all, "the first scrape, then once more after the interval, and no other scrape")
	assert.Equal(t, 7+2, len(warned(logs)), "seven files once each, and the two summary warnings")
}

func TestOneUnreadableFileAmongReadableOnesDoesNotRaiseTheSummaryWarning(t *testing.T) {
	s, logs := newTestScraper(fixtureConfig())
	src := s.src.(*fileSource)
	orig := src.open
	// The unexported open hook is the only way to simulate a read error without root.
	src.open = func(path string) ([]byte, error) {
		if filepath.Base(path) == "pages_sharing" {
			return nil, syscall.EACCES
		}
		return orig(path)
	}

	scrapeOnce(t, s)

	assert.Zero(t, logs.FilterMessageSnippet("None of the KSM files").Len())
}

func TestAMissingFileAloneIsNotAReadFailure(t *testing.T) {
	cfg := fixtureConfig()
	cfg.KsmPath = t.TempDir()
	s, logs := newTestScraper(cfg)

	scrapeOnce(t, s)

	assert.Empty(t, warned(logs))
}

// recordingConsumer keeps the context error each batch arrived with.
type recordingConsumer struct {
	mu   sync.Mutex
	errs []error
	n    int
}

func (c *recordingConsumer) ConsumeMetrics(ctx context.Context, _ pmetric.Metrics) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.n++
	c.errs = append(c.errs, ctx.Err())
	return nil
}

func (c *recordingConsumer) Capabilities() consumer.Capabilities { return consumer.Capabilities{} }

func TestAStopDuringAScrapeSendsTheBatchAndLogsNoError(t *testing.T) {
	core, logs := newObserved()
	cfg := fixtureConfig()
	ctx, cancel := context.WithCancel(context.Background())
	sc := newScraper(zap.New(core), cfg)
	src := sc.src.(*fileSource)
	orig := src.open
	// The unexported open hook is the only way to cancel the context in the middle of a read.
	src.open = func(path string) ([]byte, error) {
		cancel() // the stop arrives while the files are being read
		return orig(path)
	}
	rec := &recordingConsumer{}
	r := &ksmReceiver{logger: zap.New(core), config: cfg, consumer: rec, scraper: sc}

	r.scrapeAndSend(ctx)

	assert.Equal(t, 1, rec.n, "the batch that was read is sent")
	assert.NoError(t, rec.errs[0], "the send does not run on a canceled context")
	assert.Zero(t, logs.FilterLevelExact(zapcore.ErrorLevel).Len())
}

func TestAScrapeCanceledBeforeItStartsLogsNoError(t *testing.T) {
	core, logs := newObserved()
	cfg := fixtureConfig()
	rec := &recordingConsumer{}
	r := &ksmReceiver{logger: zap.New(core), config: cfg, consumer: rec, scraper: newScraper(zap.New(core), cfg)}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	r.scrapeAndSend(ctx)

	assert.Zero(t, rec.n)
	assert.Zero(t, logs.FilterLevelExact(zapcore.ErrorLevel).Len())
}

func TestDocsSayThatFullScansIsACounterAndTheOldSeriesWasAGauge(t *testing.T) {
	doc, err := os.ReadFile(filepath.Join("..", "..", "docs", "otel-metrics.md"))
	require.NoError(t, err)
	_, rest, ok := strings.Cut(string(doc), "### KSM")
	require.True(t, ok)
	section, _, _ := strings.Cut(rest, "\n### ")

	for _, want := range []string{"counter", "_total", "gauge", "Metricbeat"} {
		assert.Contains(t, section, want)
	}
}
