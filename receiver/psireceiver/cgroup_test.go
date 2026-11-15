package psireceiver

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

const (
	pressureBoth = "some avg10=1.00 avg60=2.00 avg300=3.00 total=100\nfull avg10=0.50 avg60=1.00 avg300=1.50 total=50\n"
	podSlice     = "kubepods.slice/kubepods-burstable.slice/kubepods-burstable-pod" + testUIDSystem + ".slice"
	containerDir = podSlice + "/cri-containerd-" + testContainer + ".scope"
)

// newRoot makes a cgroup v2 root with the controllers file.
func newRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "cgroup.controllers"), []byte("cpu memory io\n"), 0o644))
	return root
}

// addCgroup creates a cgroup directory; content "" writes all three pressure files with pressureBoth.
func addCgroup(t *testing.T, root, rel, content string) {
	t.Helper()
	dir := filepath.Join(root, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(dir, 0o755))
	if content == "" {
		content = pressureBoth
	}
	for _, res := range []string{"cpu", "memory", "io"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, res+".pressure"), []byte(content), 0o644))
	}
}

func cgroupConfig(root string, mutate ...func(*CgroupConfig)) CgroupConfig {
	cfg := defaultCgroupConfig()
	cfg.Enabled = true
	cfg.Path = root
	cfg.Include = []string{"**"}
	for _, m := range mutate {
		m(&cfg)
	}
	return cfg
}

type scrapeEnv struct {
	scraper *cgroupScraper
	logs    *observer.ObservedLogs
	reader  *metric.ManualReader
}

func newEnv(t *testing.T, cfg CgroupConfig) *scrapeEnv {
	t.Helper()
	core, logs := observer.New(zapcore.DebugLevel)
	reader := metric.NewManualReader()
	provider := metric.NewMeterProvider(metric.WithReader(reader))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	scraper, err := newCgroupScraper(zap.New(core), cfg)
	require.NoError(t, err)
	require.NoError(t, scraper.useTelemetry(component.TelemetrySettings{MeterProvider: provider}))
	return &scrapeEnv{scraper: scraper, logs: logs, reader: reader}
}

func (e *scrapeEnv) scrape() pmetric.Metrics {
	md := pmetric.NewMetrics()
	e.scraper.scrape(context.Background(), md, pcommon.Timestamp(1_000_000_000))
	return md
}

func (e *scrapeEnv) warnings() []string {
	var out []string
	for _, entry := range e.logs.All() {
		if entry.Level == zapcore.WarnLevel {
			out = append(out, entry.Message)
		}
	}
	return out
}

func (e *scrapeEnv) skippedCount(t *testing.T) int64 {
	t.Helper()
	var rm metricdata.ResourceMetrics
	require.NoError(t, e.reader.Collect(context.Background(), &rm))
	var total int64
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != "otelcol_psi_cgroups_skipped" {
				continue
			}
			for _, dp := range m.Data.(metricdata.Sum[int64]).DataPoints {
				total += dp.Value
			}
		}
	}
	return total
}

type point struct {
	metric    string
	resource  map[string]any
	attrs     map[string]any
	isSum     bool
	monotonic bool
	unit      string
	double    float64
	integer   int64
}

func points(md pmetric.Metrics) []point {
	var out []point
	for i := 0; i < md.ResourceMetrics().Len(); i++ {
		rm := md.ResourceMetrics().At(i)
		res := rm.Resource().Attributes().AsRaw()
		for j := 0; j < rm.ScopeMetrics().Len(); j++ {
			ms := rm.ScopeMetrics().At(j).Metrics()
			for k := 0; k < ms.Len(); k++ {
				m := ms.At(k)
				switch m.Type() {
				case pmetric.MetricTypeGauge:
					for d := 0; d < m.Gauge().DataPoints().Len(); d++ {
						dp := m.Gauge().DataPoints().At(d)
						out = append(out, point{metric: m.Name(), resource: res, attrs: dp.Attributes().AsRaw(), unit: m.Unit(), double: dp.DoubleValue()})
					}
				case pmetric.MetricTypeSum:
					for d := 0; d < m.Sum().DataPoints().Len(); d++ {
						dp := m.Sum().DataPoints().At(d)
						out = append(out, point{metric: m.Name(), resource: res, attrs: dp.Attributes().AsRaw(), isSum: true, monotonic: m.Sum().IsMonotonic(), unit: m.Unit(), integer: dp.IntValue()})
					}
				}
			}
		}
	}
	return out
}

func cgroupsSeen(pts []point) []string {
	seen := map[string]bool{}
	for _, p := range pts {
		seen[p.attrs["cgroup.path"].(string)] = true
	}
	var out []string
	for path := range seen {
		out = append(out, path)
	}
	sort.Strings(out)
	return out
}

func TestCgroupDisabledEmitsHostSeriesOnly(t *testing.T) {
	proc := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(proc, "pressure"), 0o755))
	for res, body := range map[string]string{
		"cpu":    "some avg10=1.00 avg60=2.00 avg300=3.00 total=100\n",
		"memory": pressureBoth,
		"io":     pressureBoth,
	} {
		require.NoError(t, os.WriteFile(filepath.Join(proc, "pressure", res), []byte(body), 0o644))
	}
	scraper, err := newPSIScraper(zap.NewNop(), &Config{ProcPath: proc})
	require.NoError(t, err)
	md, err := scraper.scrape(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, md.ResourceMetrics().Len())
	for _, p := range points(md) {
		assert.True(t, strings.HasPrefix(p.metric, "system.pressure."), p.metric)
	}
	assert.Equal(t, 15, md.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics().Len())
}

func TestCgroupBlockOnKeepsHostSeriesUnchanged(t *testing.T) {
	proc := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(proc, "pressure"), 0o755))
	for _, res := range []string{"cpu", "memory", "io"} {
		require.NoError(t, os.WriteFile(filepath.Join(proc, "pressure", res), []byte(pressureBoth), 0o644))
	}
	off, err := newPSIScraper(zap.NewNop(), &Config{ProcPath: proc})
	require.NoError(t, err)
	root := newRoot(t)
	addCgroup(t, root, podSlice, "")
	on, err := newPSIScraper(zap.NewNop(), &Config{ProcPath: proc, Cgroup: cgroupConfig(root)})
	require.NoError(t, err)

	mdOff, err := off.scrape(context.Background())
	require.NoError(t, err)
	mdOn, err := on.scrape(context.Background())
	require.NoError(t, err)

	hostOf := func(md pmetric.Metrics) []point {
		var host []point
		for _, p := range points(md) {
			if strings.HasPrefix(p.metric, "system.pressure.") {
				host = append(host, p)
			}
		}
		return host
	}
	assert.Equal(t, hostOf(mdOff), hostOf(mdOn))
	assert.Greater(t, len(points(mdOn)), len(points(mdOff)))
}

func TestMissingControllersWarnsOnce(t *testing.T) {
	root := t.TempDir()
	addCgroup(t, root, podSlice, "")
	env := newEnv(t, cgroupConfig(root))
	for i := 0; i < 3; i++ {
		assert.Empty(t, points(env.scrape()))
	}
	assert.Len(t, env.warnings(), 1, "a cgroup v1 host gets one warning, not one per interval")
}

func TestOneSeriesPerSomeAndFullLine(t *testing.T) {
	root := newRoot(t)
	addCgroup(t, root, podSlice, "")
	env := newEnv(t, cgroupConfig(root))
	pts := points(env.scrape())
	// 3 resources x (some, full) x (avg10, total)
	assert.Len(t, pts, 12)
	names := map[string]bool{}
	for _, p := range pts {
		names[p.metric] = true
	}
	for _, res := range []string{"cpu", "memory", "io"} {
		for _, kind := range []string{"some", "full"} {
			assert.True(t, names[fmt.Sprintf("cgroup.pressure.%s.%s.avg10", res, kind)])
			assert.True(t, names[fmt.Sprintf("cgroup.pressure.%s.%s.total", res, kind)])
		}
	}
}

func TestSomeOnlyFileEmitsNoFullSeries(t *testing.T) {
	root := newRoot(t)
	addCgroup(t, root, podSlice, "some avg10=1.00 avg60=2.00 avg300=3.00 total=100\n")
	env := newEnv(t, cgroupConfig(root))
	for _, p := range points(env.scrape()) {
		assert.NotContains(t, p.metric, ".full.")
	}
}

func TestMaxDepth(t *testing.T) {
	root := newRoot(t)
	addCgroup(t, root, "kubepods.slice", "")
	addCgroup(t, root, "kubepods.slice/kubepods-burstable.slice", "")
	addCgroup(t, root, podSlice, "")
	addCgroup(t, root, containerDir, "")

	deep := newEnv(t, cgroupConfig(root, func(c *CgroupConfig) { c.Level = levelAll }))
	assert.Contains(t, cgroupsSeen(points(deep.scrape())), containerDir, "the default depth 4 reaches the container scope")

	shallow := newEnv(t, cgroupConfig(root, func(c *CgroupConfig) { c.Level = levelAll; c.MaxDepth = 2 }))
	assert.Equal(t, []string{"kubepods.slice", "kubepods.slice/kubepods-burstable.slice"}, cgroupsSeen(points(shallow.scrape())))
}

func TestIncludeAndExclude(t *testing.T) {
	root := newRoot(t)
	addCgroup(t, root, "system.slice/sshd.service", "")
	addCgroup(t, root, "system.slice/cron.service", "")
	addCgroup(t, root, "user.slice/user-1000.slice", "")
	env := newEnv(t, cgroupConfig(root, func(c *CgroupConfig) {
		c.Level = levelAll
		c.Include = []string{"system.slice/**"}
		c.Exclude = []string{"**/cron.service"}
	}))
	assert.Equal(t, []string{"system.slice/sshd.service"}, cgroupsSeen(points(env.scrape())))
}

func TestLevelSelectsPodOrContainerCgroups(t *testing.T) {
	root := newRoot(t)
	addCgroup(t, root, podSlice, "")
	addCgroup(t, root, containerDir, "")
	addCgroup(t, root, "system.slice/sshd.service", "")

	run := func(level string) []string {
		env := newEnv(t, cgroupConfig(root, func(c *CgroupConfig) { c.Level = level }))
		return cgroupsSeen(points(env.scrape()))
	}
	assert.Equal(t, []string{podSlice}, run(levelPod))
	assert.Equal(t, []string{containerDir}, run(levelContainer))
	assert.ElementsMatch(t, []string{podSlice, containerDir, "system.slice/sshd.service"}, run(levelAll))
	assert.Equal(t, []string{podSlice}, cgroupsSeen(points(newEnv(t, cgroupConfig(root)).scrape())), "the default level is pod")
}

func TestValuesSelectsEmittedValues(t *testing.T) {
	root := newRoot(t)
	addCgroup(t, root, podSlice, "")

	def := newEnv(t, cgroupConfig(root))
	for _, p := range points(def.scrape()) {
		assert.True(t, strings.HasSuffix(p.metric, ".avg10") || strings.HasSuffix(p.metric, ".total"), p.metric)
	}
	only60 := newEnv(t, cgroupConfig(root, func(c *CgroupConfig) { c.Values = []string{valueAvg60} }))
	pts := points(only60.scrape())
	require.NotEmpty(t, pts)
	for _, p := range pts {
		assert.True(t, strings.HasSuffix(p.metric, ".avg60"), p.metric)
	}
}

func TestMaxCgroupsCapsAndCounts(t *testing.T) {
	root := newRoot(t)
	for i := 0; i < 501; i++ {
		addCgroup(t, root, fmt.Sprintf("kubepods.slice/kubepods-burstable.slice/kubepods-burstable-pod%s.slice", fmt.Sprintf("0a1b2c3d_4e5f_6071_8293_%012d", i)), "")
	}
	env := newEnv(t, cgroupConfig(root))
	pts := points(env.scrape())
	assert.Len(t, cgroupsSeen(pts), 500)
	assert.Len(t, env.warnings(), 1)
	assert.EqualValues(t, 1, env.skippedCount(t))
	first := cgroupsSeen(pts)[0]
	assert.Contains(t, first, "000000000000", "the first cgroups by path are kept")
}

func TestMetricNamesTypesAndUnits(t *testing.T) {
	root := newRoot(t)
	addCgroup(t, root, podSlice, "")
	env := newEnv(t, cgroupConfig(root, func(c *CgroupConfig) {
		c.Values = []string{valueAvg10, valueAvg60, valueAvg300, valueTotal}
	}))
	for _, p := range points(env.scrape()) {
		assert.True(t, strings.HasPrefix(p.metric, "cgroup.pressure."), p.metric)
		if strings.HasSuffix(p.metric, ".total") {
			assert.True(t, p.isSum && p.monotonic, p.metric)
			assert.Equal(t, "us", p.unit)
			assert.EqualValues(t, map[bool]int64{true: 100, false: 50}[strings.Contains(p.metric, ".some.")], p.integer)
		} else {
			assert.False(t, p.isSum, p.metric)
			assert.Equal(t, "%", p.unit)
		}
	}
}

func TestPodAndContainerAttributes(t *testing.T) {
	root := newRoot(t)
	addCgroup(t, root, podSlice, "")
	addCgroup(t, root, containerDir, "")
	addCgroup(t, root, "system.slice/sshd.service", "")
	addCgroup(t, root, "kubepods/besteffort/pod"+testUID, "")
	env := newEnv(t, cgroupConfig(root, func(c *CgroupConfig) { c.Level = levelAll }))
	byPath := map[string]point{}
	for _, p := range points(env.scrape()) {
		byPath[p.attrs["cgroup.path"].(string)] = p
	}

	pod := byPath[podSlice]
	assert.Equal(t, testUID, pod.resource["k8s.pod.uid"])
	assert.Equal(t, "burstable", pod.resource["k8s.pod.qos"])
	assert.NotContains(t, pod.attrs, "container.id")

	ctr := byPath[containerDir]
	assert.Equal(t, testUID, ctr.resource["k8s.pod.uid"])
	assert.Equal(t, testContainer, ctr.attrs["container.id"])

	cgroupfs := byPath["kubepods/besteffort/pod"+testUID]
	assert.Equal(t, testUID, cgroupfs.resource["k8s.pod.uid"], "the cgroupfs layout gives the same uid")
	assert.Equal(t, "besteffort", cgroupfs.resource["k8s.pod.qos"])

	svc := byPath["system.slice/sshd.service"]
	assert.Empty(t, svc.resource, "a path that matches no layout has no resource attributes")
	assert.Equal(t, map[string]any{"cgroup.path": "system.slice/sshd.service"}, svc.attrs)
}

func TestVanishedAndEmptyCgroupsAreSkippedQuietly(t *testing.T) {
	root := newRoot(t)
	addCgroup(t, root, podSlice, "")
	// listed, but its pressure files are gone: a cgroup removed after the listing
	require.NoError(t, os.MkdirAll(filepath.Join(root, "kubepods.slice/kubepods-burstable.slice/kubepods-burstable-pod"+strings.ReplaceAll(testUIDSystem, "0a1b", "1a1b")+".slice"), 0o755))
	// opted out: the files read empty
	addCgroup(t, root, "kubepods.slice/kubepods-pod"+strings.ReplaceAll(testUIDSystem, "0a1b", "2a1b")+".slice", " ")
	require.NoError(t, os.WriteFile(filepath.Join(root, "kubepods.slice/kubepods-pod"+strings.ReplaceAll(testUIDSystem, "0a1b", "2a1b")+".slice/cpu.pressure"), nil, 0o644))

	env := newEnv(t, cgroupConfig(root))
	pts := points(env.scrape())
	assert.Equal(t, []string{podSlice}, cgroupsSeen(pts))
	assert.Empty(t, env.warnings())
}

func TestEveryReadFailingWarnsOncePerInterval(t *testing.T) {
	root := newRoot(t)
	for i := 0; i < 3; i++ {
		rel := fmt.Sprintf("kubepods.slice/kubepods-pod0a1b2c3d_4e5f_6071_8293_%012d.slice", i)
		for _, res := range []string{"cpu", "memory", "io"} {
			// a directory where the file should be: reading it fails and is not "missing"
			require.NoError(t, os.MkdirAll(filepath.Join(root, rel, res+".pressure"), 0o755))
		}
	}
	env := newEnv(t, cgroupConfig(root))
	assert.Empty(t, points(env.scrape()))
	assert.Len(t, env.warnings(), 1, "one warning for the interval, not one per file")
	env.scrape()
	assert.Len(t, env.warnings(), 2, "the next interval warns again")
}

func TestMissingPathWarnsOnceAndRecovers(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "cgroup")
	env := newEnv(t, cgroupConfig(root))
	assert.Empty(t, points(env.scrape()))
	assert.Empty(t, points(env.scrape()))
	assert.Len(t, env.warnings(), 1)

	require.NoError(t, os.MkdirAll(root, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "cgroup.controllers"), []byte("cpu\n"), 0o644))
	addCgroup(t, root, podSlice, "")
	assert.NotEmpty(t, points(env.scrape()), "the cgroups are read at the interval after the path appears")
}

func TestMalformedLineEmitsNoSeries(t *testing.T) {
	root := newRoot(t)
	addCgroup(t, root, podSlice, "some avg10=oops avg60=2.00 avg300=3.00 total=100\n")
	env := newEnv(t, cgroupConfig(root))
	assert.Empty(t, points(env.scrape()), "a line that does not parse must not become zero-valued samples")
	assert.Empty(t, env.warnings())
}

func TestUnreadableDirectoryWarnsOncePerEpisode(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("directory permissions do not stop root")
	}
	root := newRoot(t)
	addCgroup(t, root, podSlice, "")
	locked := filepath.Join(root, "kubepods.slice", "locked.slice")
	require.NoError(t, os.MkdirAll(locked, 0o755))
	require.NoError(t, os.Chmod(locked, 0))
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	env := newEnv(t, cgroupConfig(root))
	assert.Equal(t, []string{podSlice}, cgroupsSeen(points(env.scrape())), "the readable cgroups are still emitted")
	env.scrape()
	assert.Len(t, env.warnings(), 1, "the same condition is not warned about at every interval")

	require.NoError(t, os.Chmod(locked, 0o755))
	env.scrape()
	assert.Len(t, env.warnings(), 1, "no warning while everything can be listed")

	require.NoError(t, os.Chmod(locked, 0))
	env.scrape()
	assert.Len(t, env.warnings(), 2, "a new episode warns again")
}

func TestIsGone(t *testing.T) {
	assert.True(t, isGone(&fs.PathError{Op: "open", Path: "x", Err: syscall.ENOENT}))
	assert.True(t, isGone(&fs.PathError{Op: "read", Path: "x", Err: syscall.ENODEV}), "a cgroup removed between open and read")
	assert.True(t, isGone(&fs.PathError{Op: "read", Path: "x", Err: syscall.EOPNOTSUPP}), "pressure accounting off for the cgroup")
	assert.False(t, isGone(&fs.PathError{Op: "open", Path: "x", Err: syscall.EACCES}))
	assert.False(t, isGone(errors.New("some other failure")))
}

func TestNoOsExecInCgroupCode(t *testing.T) {
	files, err := filepath.Glob("cgroup*.go")
	require.NoError(t, err)
	require.NotEmpty(t, files)
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		require.NoError(t, err)
		assert.NotContains(t, string(src), `"os/exec"`, f)
	}
}

func TestCgroupConfigValidation(t *testing.T) {
	base := func(mutate func(*Config)) Config {
		cfg := Config{CollectionInterval: 60_000_000_000, ProcPath: "/proc", Cgroup: defaultCgroupConfig()}
		cfg.Cgroup.Enabled = true
		cfg.Cgroup.Include = []string{"kubepods.slice/**"}
		mutate(&cfg)
		return cfg
	}
	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr string
	}{
		{"valid", func(*Config) {}, ""},
		{"disabled needs no include", func(c *Config) { c.Cgroup.Enabled = false; c.Cgroup.Include = nil }, ""},
		{"empty include", func(c *Config) { c.Cgroup.Include = nil }, "include"},
		{"bad glob", func(c *Config) { c.Cgroup.Include = []string{"[a"} }, "include"},
		{"bad exclude glob", func(c *Config) { c.Cgroup.Exclude = []string{"[a"} }, "exclude"},
		{"bad level", func(c *Config) { c.Cgroup.Level = "node" }, "level"},
		{"bad value", func(c *Config) { c.Cgroup.Values = []string{"avg1"} }, "values"},
		{"empty values", func(c *Config) { c.Cgroup.Values = nil }, "values"},
		{"zero depth", func(c *Config) { c.Cgroup.MaxDepth = 0 }, "max_depth"},
		{"zero cap", func(c *Config) { c.Cgroup.MaxCgroups = 0 }, "max_cgroups"},
		{"empty path", func(c *Config) { c.Cgroup.Path = "" }, "path"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := base(tt.mutate)
			err := cfg.Validate()
			if tt.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestCgroupDefaults(t *testing.T) {
	cfg := createDefaultConfig().(*Config)
	assert.False(t, cfg.Cgroup.Enabled)
	assert.Equal(t, "/sys/fs/cgroup", cfg.Cgroup.Path)
	assert.Equal(t, 4, cfg.Cgroup.MaxDepth)
	assert.Equal(t, levelPod, cfg.Cgroup.Level)
	assert.Equal(t, []string{valueAvg10, valueTotal}, cfg.Cgroup.Values)
	assert.Equal(t, 500, cfg.Cgroup.MaxCgroups)
	assert.Empty(t, cfg.Cgroup.Include)
	assert.NoError(t, cfg.Validate(), "a default config with the block off is valid")
}
