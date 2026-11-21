// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

// cspell:ignore hwmap hwmapprocessor milli

package hwmapprocessor

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/processor/processortest"
)

func TestNoExec_NoSourceFileImportsAPackageThatRunsACommand(t *testing.T) {
	files, err := filepath.Glob("*.go")
	require.NoError(t, err)
	checked := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		checked++
		parsed, err := parser.ParseFile(token.NewFileSet(), f, nil, parser.ImportsOnly)
		require.NoError(t, err)
		for _, imp := range parsed.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			assert.NotEqual(t, "os/exec", path, f)
			assert.NotEqual(t, "syscall", path, f)
			assert.False(t, strings.HasPrefix(path, "plugin"), f)
		}
	}
	assert.Greater(t, checked, 3, "the check found the source files")
}

// Only the scrape manifest lists the processor.
func TestManifest_OnlyTheScrapeImageHasHwmap(t *testing.T) {
	manifests, err := filepath.Glob("../../builder-*.yaml")
	require.NoError(t, err)
	require.NotEmpty(t, manifests)
	for _, m := range manifests {
		raw, err := os.ReadFile(m)
		require.NoError(t, err)
		has := strings.Contains(string(raw), "processor/hwmapprocessor")
		if filepath.Base(m) == "builder-scrape.yaml" {
			lines := 0
			for _, line := range strings.Split(string(raw), "\n") {
				if strings.Contains(line, "processor/hwmapprocessor") {
					lines++
				}
			}
			assert.Equal(t, 2, lines, "the replace line and the processor line")
			continue
		}
		assert.False(t, has, "%s must not list hwmap", filepath.Base(m))
	}
}

func TestDocs_SizeMinusFreeUsageIsListedAsNotSupported(t *testing.T) {
	raw, err := os.ReadFile("../../docs/hwmap.md")
	require.NoError(t, err)
	found := false
	for _, line := range strings.Split(string(raw), "\n") {
		l := strings.ToLower(line)
		if strings.Contains(l, "size minus free") && strings.Contains(l, "not supported") {
			found = true
		}
	}
	assert.True(t, found, "docs/hwmap.md says the size-minus-free usage is not supported")
}

// Through the factory, with no rule nothing changes and with a rule the rule applies.
func TestFactory_OffUntilARuleIsConfigured(t *testing.T) {
	set := processortest.NewNopSettings(NewFactory().Type())
	input := func() pmetric.Metrics { return build(nil, gaugeOf("test.sensor.milli", "mCel", ip(45000))) }

	off := new(consumertest.MetricsSink)
	p, err := NewFactory().CreateMetrics(t.Context(), set, NewFactory().CreateDefaultConfig(), off)
	require.NoError(t, err)
	require.NoError(t, p.ConsumeMetrics(t.Context(), input()))
	assert.Equal(t, flatten(input()), flatten(off.AllMetrics()[0]))

	on := new(consumertest.MetricsSink)
	cfg := &Config{Rules: []Rule{tempRule(nil)}}
	require.NoError(t, cfg.Validate())
	p, err = NewFactory().CreateMetrics(t.Context(), set, cfg, on)
	require.NoError(t, err)
	require.NoError(t, p.ConsumeMetrics(t.Context(), input()))
	got := flatten(on.AllMetrics()[0])
	require.Len(t, got, 1)
	assert.Equal(t, "hw.temperature", got[0].metric)
	assert.True(t, p.Capabilities().MutatesData)
	assert.NoError(t, p.Start(t.Context(), nil))
	assert.NoError(t, p.Shutdown(t.Context()))
}

func TestFactory_DefaultConfigValidates(t *testing.T) {
	assert.NoError(t, NewFactory().CreateDefaultConfig().(*Config).Validate())
	assert.Equal(t, "hwmap", NewFactory().Type().String())
}

// Robustness: a very large rule set loads and runs.
func TestHugeRuleSets_ManyMetricsAndManyRulesOnOneMetric(t *testing.T) {
	cfg := &Config{}
	for i := range 20000 {
		cfg.Rules = append(cfg.Rules, plain("many-"+strconv.Itoa(i), "metric."+strconv.Itoa(i), "target."+strconv.Itoa(i)))
	}
	for i := range 2000 {
		r := plain("same-"+strconv.Itoa(i), "shared", "shared.out")
		r.Match.Attributes = map[string]string{"k": strconv.Itoa(i)}
		cfg.Rules = append(cfg.Rules, r)
	}
	h := newHarness(t, cfg)
	var pts []pt
	for i := range 1000 {
		pts = append(pts, ip(int64(i), "k", strconv.Itoa(i*3)))
	}

	start := time.Now()
	got := h.run(build(nil, gaugeOf("shared", "1", pts...), gaugeOf("metric.19999", "1", ip(1))))

	assert.Less(t, time.Since(start), 5*time.Second)
	assert.Len(t, named(got, "target.19999"), 1)
	assert.Len(t, named(got, "shared.out"), 667, "k = 0, 3, ... 1998 matches a rule")
	assert.Len(t, named(got, "shared"), 333)
}

func benchBatch() pmetric.Metrics {
	var pts []pt
	for i := range 1000 {
		pts = append(pts, ip(int64(i*1000), "sensor.kind", "temp", "idx", strconv.Itoa(i)))
	}
	return build(map[string]string{"host.name": "h"}, gaugeOf("test.sensor.milli", "mCel", pts...), gaugeOf("other.metric", "1", pts[:200]...))
}

func benchRules() *Config {
	cfg := &Config{}
	for i := range 20 {
		r := tempRule(map[string]string{"sensor.kind": "temp"})
		r.Name = "r" + strconv.Itoa(i)
		r.Match.Metric = "metric.unused." + strconv.Itoa(i)
		cfg.Rules = append(cfg.Rules, r)
	}
	r := tempRule(map[string]string{"sensor.kind": "temp"})
	r.Labels = map[string]string{"hw.type": "temperature", "hw.id": "{idx}"}
	cfg.Rules = append(cfg.Rules, r)
	return cfg
}

// BenchmarkNoRules is the no-op base: the same copy and consume with no rule.
func BenchmarkNoRules(b *testing.B) {
	benchRun(b, &Config{})
}

// BenchmarkRules maps 1000 points of one metric and passes 200 of another.
func BenchmarkRules(b *testing.B) {
	benchRun(b, benchRules())
}

func benchRun(b *testing.B, cfg *Config) {
	b.Helper()
	set := processortest.NewNopSettings(NewFactory().Type())
	p, err := NewFactory().CreateMetrics(b.Context(), set, cfg, consumertest.NewNop())
	require.NoError(b, err)
	src := benchBatch()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		md := pmetric.NewMetrics()
		src.CopyTo(md)
		_ = p.ConsumeMetrics(b.Context(), md)
	}
}
