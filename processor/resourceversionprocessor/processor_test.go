// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package resourceversionprocessor

import (
	"context"
	"testing"

	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/ptrace"
)

const testFQDN = "web01.example.net"

func hostCfg(short bool) *Config {
	return &Config{Role: "edge", ShortHostName: short}
}

// seed fills a resource with the given host.name and a host.id that must stay as it is.
func seed(attrs pcommon.Map, host string) {
	if host != "" {
		attrs.PutStr("host.name", host)
	}
	attrs.PutStr("host.id", "id-"+testFQDN)
}

func hostOf(t *testing.T, attrs pcommon.Map) string {
	t.Helper()
	v, ok := attrs.Get("host.name")
	if !ok {
		return ""
	}
	return v.Str()
}

func checkResource(t *testing.T, attrs pcommon.Map, wantHost string) {
	t.Helper()
	if got := hostOf(t, attrs); got != wantHost {
		t.Errorf("host.name = %q, want %q", got, wantHost)
	}
	if v, _ := attrs.Get("host.id"); v.Str() != "id-"+testFQDN {
		t.Errorf("host.id = %q, want it unchanged", v.Str())
	}
	if _, ok := attrs.Get("collector.edge.version"); !ok {
		t.Error("collector.edge.version missing")
	}
}

func hostCases() []struct {
	name string
	cfg  *Config
	in   string
	want string
} {
	return []struct {
		name string
		cfg  *Config
		in   string
		want string
	}{
		{"off keeps the FQDN", hostCfg(false), testFQDN, testFQDN},
		{"on shortens the FQDN", hostCfg(true), testFQDN, "web01"},
		{"on keeps a short name", hostCfg(true), "web01", "web01"},
		{"on keeps an IPv4 address", hostCfg(true), "192.0.2.10", "192.0.2.10"},
		{"on keeps an IPv6 address", hostCfg(true), "2001:db8::1", "2001:db8::1"},
		{"on adds nothing when host.name is absent", hostCfg(true), "", ""},
	}
}

// runHostCases runs every host case through one signal. run sends a seeded
// resource through that signal's processor and returns the resource attributes
// the next consumer received.
func runHostCases(t *testing.T, run func(t *testing.T, cfg *Config, host string) pcommon.Map) {
	t.Helper()
	for _, tc := range hostCases() {
		t.Run(tc.name, func(t *testing.T) {
			checkResource(t, run(t, tc.cfg, tc.in), tc.want)
		})
	}
}

func TestMetricsShortHostName(t *testing.T) {
	runHostCases(t, func(t *testing.T, cfg *Config, host string) pcommon.Map {
		var got pmetric.Metrics
		next, err := consumer.NewMetrics(func(_ context.Context, md pmetric.Metrics) error {
			got = md
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		md := pmetric.NewMetrics()
		seed(md.ResourceMetrics().AppendEmpty().Resource().Attributes(), host)
		if err := newMetricsProcessor(cfg, next).ConsumeMetrics(context.Background(), md); err != nil {
			t.Fatal(err)
		}
		return got.ResourceMetrics().At(0).Resource().Attributes()
	})
}

func TestLogsShortHostName(t *testing.T) {
	runHostCases(t, func(t *testing.T, cfg *Config, host string) pcommon.Map {
		var got plog.Logs
		next, err := consumer.NewLogs(func(_ context.Context, ld plog.Logs) error {
			got = ld
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		ld := plog.NewLogs()
		seed(ld.ResourceLogs().AppendEmpty().Resource().Attributes(), host)
		if err := newLogsProcessor(cfg, next).ConsumeLogs(context.Background(), ld); err != nil {
			t.Fatal(err)
		}
		return got.ResourceLogs().At(0).Resource().Attributes()
	})
}

func TestTracesShortHostName(t *testing.T) {
	runHostCases(t, func(t *testing.T, cfg *Config, host string) pcommon.Map {
		var got ptrace.Traces
		next, err := consumer.NewTraces(func(_ context.Context, td ptrace.Traces) error {
			got = td
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		td := ptrace.NewTraces()
		seed(td.ResourceSpans().AppendEmpty().Resource().Attributes(), host)
		if err := newTracesProcessor(cfg, next).ConsumeTraces(context.Background(), td); err != nil {
			t.Fatal(err)
		}
		return got.ResourceSpans().At(0).Resource().Attributes()
	})
}

func TestShortHostNameEveryResourceAndOnlyStrings(t *testing.T) {
	var got pmetric.Metrics
	next, err := consumer.NewMetrics(func(_ context.Context, md pmetric.Metrics) error {
		got = md
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	p := newMetricsProcessor(hostCfg(true), next)
	md := pmetric.NewMetrics()
	md.ResourceMetrics().AppendEmpty().Resource().Attributes().PutStr("host.name", "a.example.net")
	md.ResourceMetrics().AppendEmpty().Resource().Attributes().PutStr("host.name", "b.example.net")
	md.ResourceMetrics().AppendEmpty().Resource().Attributes().PutInt("host.name", 7)
	if err := p.ConsumeMetrics(context.Background(), md); err != nil {
		t.Fatal(err)
	}
	rms := got.ResourceMetrics()
	if h := hostOf(t, rms.At(0).Resource().Attributes()); h != "a" {
		t.Errorf("resource 0 host.name = %q, want a", h)
	}
	if h := hostOf(t, rms.At(1).Resource().Attributes()); h != "b" {
		t.Errorf("resource 1 host.name = %q, want b", h)
	}
	if v, _ := rms.At(2).Resource().Attributes().Get("host.name"); v.Type() != pcommon.ValueTypeInt || v.Int() != 7 {
		t.Errorf("a non-string host.name changed to %v", v.AsRaw())
	}
}

func TestDefaultConfigKeepsTheFullHostName(t *testing.T) {
	cfg, ok := NewFactory().CreateDefaultConfig().(*Config)
	if !ok {
		t.Fatal("default config is not *Config")
	}
	if cfg.ShortHostName {
		t.Error("short_host_name is on by default")
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("validate: %v", err)
	}
}
