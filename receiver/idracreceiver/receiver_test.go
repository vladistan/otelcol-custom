// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package idracreceiver

import (
	"context"
	"go/ast"
	"go/parser"
	gotoken "go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
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
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/vladistan/otelcol-custom/receiver/hwschema"
)

func settings() (receiver.Settings, *sdkmetric.ManualReader) {
	set := receivertest.NewNopSettings(component.MustNewType("idrac"))
	reader := sdkmetric.NewManualReader()
	set.MeterProvider = sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	return set, reader
}

func twoTargetConfig(a, b *fakeRedfish) *Config {
	cfg := createDefaultConfig().(*Config)
	cfg.CollectionInterval = time.Second
	for i, f := range []*fakeRedfish{a, b} {
		t := validTarget()
		t.Name = []string{"db-1", "db-2"}[i]
		t.Address = f.srv.URL
		t.InsecureSkipVerify = true
		cfg.Targets = append(cfg.Targets, t)
	}
	return cfg
}

func TestTwoTargetsGiveTwoResourcesWithTheirOwnHostName(t *testing.T) {
	a, b := newFakeRedfish(), newFakeRedfish()
	defer a.close()
	defer b.close()
	set, _ := settings()
	sink := new(consumertest.MetricsSink)
	r, err := newReceiver(set, twoTargetConfig(a, b), sink)
	require.NoError(t, err)
	r.offset = func(string, time.Duration) time.Duration { return 0 }

	require.NoError(t, r.Start(context.Background(), componenttest.NewNopHost()))
	assert.Eventually(t, func() bool { return len(sink.AllMetrics()) >= 2 }, 10*time.Second, 20*time.Millisecond)
	require.NoError(t, r.Shutdown(context.Background()))

	hosts := map[string]bool{}
	for _, md := range sink.AllMetrics() {
		require.Equal(t, 1, md.ResourceMetrics().Len())
		h, _ := md.ResourceMetrics().At(0).Resource().Attributes().Get("host.name")
		hosts[h.Str()] = true
	}
	assert.Equal(t, map[string]bool{"db-1": true, "db-2": true}, hosts)
}

func TestShutdownDeletesEachSessionAndScrapesRepeat(t *testing.T) {
	a, b := newFakeRedfish(), newFakeRedfish()
	defer a.close()
	defer b.close()
	set, _ := settings()
	sink := new(consumertest.MetricsSink)
	r, err := newReceiver(set, twoTargetConfig(a, b), sink)
	require.NoError(t, err)
	r.offset = func(string, time.Duration) time.Duration { return 0 }

	require.NoError(t, r.Start(context.Background(), componenttest.NewNopHost()))
	assert.Eventually(t, func() bool { return len(sink.AllMetrics()) >= 4 }, 10*time.Second, 20*time.Millisecond)
	require.NoError(t, r.Shutdown(context.Background()))

	assert.Equal(t, 1, a.deletes)
	assert.Equal(t, 1, b.deletes)
	assert.Equal(t, 1, a.logins, "the session is reused across scrapes")
}

func TestStartOffsetsDifferByNameAndAreStable(t *testing.T) {
	a, b := startOffset("db-1", time.Minute), startOffset("db-2", time.Minute)

	assert.NotEqual(t, a, b)
	assert.Equal(t, a, startOffset("db-1", time.Minute))
	assert.Less(t, a, time.Minute)
}

func TestShutdownWithoutStartIsClean(t *testing.T) {
	a, b := newFakeRedfish(), newFakeRedfish()
	defer a.close()
	defer b.close()
	set, _ := settings()
	r, err := newReceiver(set, twoTargetConfig(a, b), consumertest.NewNop())
	require.NoError(t, err)

	require.NoError(t, r.Shutdown(context.Background()))
	assert.Equal(t, 0, a.deletes)
}

func TestAuthFailuresAreCountedPerTargetInTheCollectorsOwnTelemetry(t *testing.T) {
	a, b := newFakeRedfish(), newFakeRedfish()
	defer a.close()
	defer b.close()
	a.loginStatus = 401
	set, reader := settings()
	r, err := newReceiver(set, twoTargetConfig(a, b), consumertest.NewNop())
	require.NoError(t, err)
	r.offset = func(string, time.Duration) time.Duration { return 0 }

	require.NoError(t, r.Start(context.Background(), componenttest.NewNopHost()))
	assert.Eventually(t, func() bool { return authFailureCount(t, reader, "db-1") >= 1 }, 10*time.Second, 20*time.Millisecond)
	require.NoError(t, r.Shutdown(context.Background()))

	assert.Equal(t, int64(1), authFailureCount(t, reader, "db-1"))
	assert.Equal(t, int64(0), authFailureCount(t, reader, "db-2"))
}

func authFailureCount(t *testing.T, reader sdkmetric.Reader, bmc string) int64 {
	t.Helper()
	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != "otelcol_hw_bmc_auth_failures" {
				continue
			}
			sum, ok := m.Data.(metricdata.Sum[int64])
			require.True(t, ok)
			for _, dp := range sum.DataPoints {
				if v, found := dp.Attributes.Value("bmc"); found && v.AsString() == bmc {
					return dp.Value
				}
			}
		}
	}
	return 0
}

// Factory.

func TestFactoryTypeAndStability(t *testing.T) {
	f := NewFactory()

	assert.Equal(t, "idrac", f.Type().String())
	assert.Equal(t, component.StabilityLevelDevelopment, f.MetricsStability())
}

func TestFactoryCreatesMetricsOnlyAndRejectsAConfigWithoutTargets(t *testing.T) {
	f := NewFactory()
	set := receivertest.NewNopSettings(f.Type())

	_, logsErr := f.CreateLogs(context.Background(), set, f.CreateDefaultConfig(), consumertest.NewNop())
	_, metricsErr := f.CreateMetrics(context.Background(), set, f.CreateDefaultConfig(), consumertest.NewNop())

	assert.Error(t, logsErr)
	require.Error(t, metricsErr)
	assert.Contains(t, metricsErr.Error(), "targets")
}

func TestFactoryRejectsABadCAFileAtCreation(t *testing.T) {
	f := NewFactory()
	cfg := f.CreateDefaultConfig().(*Config)
	tgt := validTarget()
	tgt.CAFile = filepath.Join(t.TempDir(), "absent.pem")
	cfg.Targets = []TargetConfig{tgt}

	_, err := f.CreateMetrics(context.Background(), receivertest.NewNopSettings(f.Type()), cfg, consumertest.NewNop())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "ca_file")
}

func TestFactoryCreatesTheReceiverFromAValidConfig(t *testing.T) {
	f := NewFactory()
	cfg := f.CreateDefaultConfig().(*Config)
	cfg.Targets = validConfig().Targets

	r, err := f.CreateMetrics(context.Background(), receivertest.NewNopSettings(f.Type()), cfg, consumertest.NewNop())

	require.NoError(t, err)
	require.NoError(t, r.Shutdown(context.Background()))
}

// The modern thermal subsystem.

func TestThermalSubsystemWhenTheBMCServesIt(t *testing.T) {
	f := newFakeRedfish()
	defer f.close()
	f.remove(chassis + "/Thermal")
	f.set(chassis+"/ThermalSubsystem", obj{
		"ThermalMetrics": ref(chassis + "/ThermalSubsystem/ThermalMetrics"),
		"Fans":           ref(chassis + "/ThermalSubsystem/Fans"),
	})
	f.set(chassis+"/ThermalSubsystem/ThermalMetrics", obj{"TemperatureReadingsCelsius": []obj{
		{"DeviceName": "System Board Inlet Temp", "Reading": 23.5},
	}})
	f.set(chassis+"/ThermalSubsystem/Fans", obj{"Members": []obj{
		{"Id": "Fan.1", "Name": "Fan 1", "SpeedPercent": obj{"Reading": 55}, "Status": obj{"Health": "OK"}},
	}})
	r := newRig(t, f, nil)

	md := r.scrape(t)

	assert.Equal(t, 1, pointCount(md, "hw.temperature"))
	assert.InDelta(t, 23.5, dataPoints(md, "hw.temperature").At(0).DoubleValue(), 1e-9)
	assert.InDelta(t, 0.55, dataPoints(md, "hw.fan.speed_ratio").At(0).DoubleValue(), 1e-9)
	assert.Equal(t, 1, f.count("GET", chassis+"/Thermal"), "the legacy Thermal is asked first and answered 404")
}

// Source rules: no exec, no cgo, and a metric name only in hwschema.

func sourceViolations(files map[string]string) []string {
	var out []string
	metricNames := map[string]bool{}
	for _, n := range hwschema.BMCMetricNames() {
		metricNames[n] = true
	}
	for name, src := range files {
		f, err := parser.ParseFile(gotoken.NewFileSet(), name, src, 0)
		if err != nil {
			out = append(out, name+": does not parse")
			continue
		}
		for _, imp := range f.Imports {
			if path, _ := strconv.Unquote(imp.Path.Value); path == "os/exec" || path == "C" {
				out = append(out, name+": imports "+path)
			}
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == gotoken.STRING {
				if s, err := strconv.Unquote(lit.Value); err == nil && metricNames[s] {
					out = append(out, name+": writes the metric name "+s)
				}
			}
			return true
		})
	}
	sort.Strings(out)
	return out
}

func TestTheReceiverSourcePassesItsOwnRules(t *testing.T) {
	files := map[string]string{}
	entries, err := os.ReadDir(".")
	require.NoError(t, err)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		b, err := os.ReadFile(filepath.Clean(e.Name()))
		require.NoError(t, err)
		files[e.Name()] = string(b)
	}

	assert.Empty(t, sourceViolations(files))
}

func TestSourceRuleFindsExecCgoAndAMetricNameOutsideHwschema(t *testing.T) {
	got := sourceViolations(map[string]string{"x.go": `package x
import "os/exec"
var _ = exec.Command
var name = "hw.temperature"`})

	assert.Contains(t, got, "x.go: imports os/exec")
	assert.Contains(t, got, "x.go: writes the metric name hw.temperature")
}

func TestTheTargetBuildsEveryPointThroughHwschema(t *testing.T) {
	b, err := os.ReadFile("target.go")
	require.NoError(t, err)

	assert.Contains(t, string(b), `"github.com/vladistan/otelcol-custom/receiver/hwschema"`)
}
