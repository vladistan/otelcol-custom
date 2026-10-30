// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package healthprobesampler

import (
	"context"
	"math/rand/v2"
	"testing"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/processor/processortest"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func seeded() func() float64 { return rand.New(rand.NewPCG(1, 2)).Float64 }

func withRatio(ratio float64) *Config {
	cfg := NewFactory().CreateDefaultConfig().(*Config)
	cfg.KeepRatio = ratio
	return cfg
}

func start(t *testing.T, cfg *Config, draw func() float64) (*processorImpl, *consumertest.LogsSink) {
	t.Helper()
	sink := new(consumertest.LogsSink)
	set := processortest.NewNopSettings(component.MustNewType(typeStr))
	p, err := newProcessor(cfg, set.TelemetrySettings, sink, draw)
	if err != nil {
		t.Fatalf("newProcessor: %v", err)
	}
	return p, sink
}

// shape is one parser's output: how the record carries the three standard attributes.
type shape struct {
	name   string
	status func(attrs pcommon.Map, code int)
	agent  bool // false when the parser output has no user_agent.original
}

var shapes = []shape{
	{"nginx", func(a pcommon.Map, c int) { a.PutInt("http.response.status_code", int64(c)) }, true},
	{"sentry", func(a pcommon.Map, c int) { a.PutStr("http.response.status_code", itoa(c)) }, true},
	{"uvicorn", func(a pcommon.Map, c int) { a.PutInt("http.response.status_code", int64(c)) }, false},
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}

func logRecord(s shape, status int, path, agent string) plog.Logs {
	ld := plog.NewLogs()
	lr := ld.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty().LogRecords().AppendEmpty()
	lr.Body().SetStr("200 GET " + path)
	a := lr.Attributes()
	a.PutStr("original_message", "raw line for "+path)
	a.PutStr("full_message", "full line for "+path)
	if path != "" {
		a.PutStr("url.path", path)
	}
	if agent != "" && s.agent {
		a.PutStr("user_agent.original", agent)
	}
	s.status(a, status)
	return ld
}

func consume(t *testing.T, p *processorImpl, ld plog.Logs) {
	t.Helper()
	if err := p.ConsumeLogs(context.Background(), ld); err != nil {
		t.Fatalf("consume: %v", err)
	}
}

func marshal(t *testing.T, ld plog.Logs) string {
	t.Helper()
	b, err := (&plog.JSONMarshaler{}).MarshalLogs(ld)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

func onlyRecord(t *testing.T, sink *consumertest.LogsSink) plog.LogRecord {
	t.Helper()
	if sink.LogRecordCount() != 1 {
		t.Fatalf("sink saw %d records, want 1", sink.LogRecordCount())
	}
	return sink.AllLogs()[0].ResourceLogs().At(0).ScopeLogs().At(0).LogRecords().At(0)
}

func TestKeptProbe_EachPathAndStatus_IsRelabelled(t *testing.T) {
	paths := []string{"/healthz", "/health", "/api/health", "/probe", "/api/healthz"}
	for _, s := range shapes {
		for _, path := range paths {
			for _, status := range []int{200, 302} {
				t.Run(s.name+path+itoa(status), func(t *testing.T) {
					p, sink := start(t, withRatio(1), seeded())

					consume(t, p, logRecord(s, status, path, ""))

					lr := onlyRecord(t, sink)
					if lr.Body().Str() != "HealthProbe" {
						t.Errorf("body = %q, want HealthProbe", lr.Body().Str())
					}
					if v, _ := lr.Attributes().Get("log.type"); v.Str() != "health_probe" {
						t.Errorf("log.type = %q, want health_probe", v.Str())
					}
				})
			}
		}
	}
}

func TestKeptProbe_EachAgentPrefixOnOtherPath_IsRelabelled(t *testing.T) {
	for _, agent := range []string{"kube-probe/1.29", "ELB-HealthChecker/2.0"} {
		for _, s := range shapes {
			if !s.agent {
				continue
			}
			t.Run(s.name+agent, func(t *testing.T) {
				p, sink := start(t, withRatio(1), seeded())

				consume(t, p, logRecord(s, 200, "/orders", agent))

				if lr := onlyRecord(t, sink); lr.Body().Str() != "HealthProbe" {
					t.Errorf("body = %q, want HealthProbe", lr.Body().Str())
				}
			})
		}
	}
}

func TestProbePathWithQuery_IsClassified(t *testing.T) {
	p, sink := start(t, withRatio(0), seeded())

	consume(t, p, logRecord(shapes[0], 200, "/healthz?x=1", ""))

	if sink.LogRecordCount() != 0 {
		t.Error("probe with a query string was not dropped at ratio 0")
	}
}

func TestNonProbe_PassesUnchanged(t *testing.T) {
	for _, s := range shapes {
		t.Run(s.name, func(t *testing.T) {
			p, sink := start(t, withRatio(0), seeded())
			in := logRecord(s, 200, "/orders", "curl/8.0")
			want := marshal(t, in)

			consume(t, p, in)

			if len(sink.AllLogs()) != 1 || marshal(t, sink.AllLogs()[0]) != want {
				t.Error("non-probe record changed")
			}
		})
	}
}

func TestProbeStatus4xx5xx_KeptUnchangedAtRatioZero(t *testing.T) {
	for _, s := range shapes {
		for _, status := range []int{404, 500, 503} {
			t.Run(s.name+itoa(status), func(t *testing.T) {
				p, sink := start(t, withRatio(0), seeded())
				in := logRecord(s, status, "/healthz", "kube-probe/1.29")
				want := marshal(t, in)

				consume(t, p, in)

				if len(sink.AllLogs()) != 1 || marshal(t, sink.AllLogs()[0]) != want {
					t.Error("probe-path record with an error status changed or was dropped")
				}
			})
		}
	}
}

func TestStatus301And304_AreNotProbes(t *testing.T) {
	for _, status := range []int{301, 304, 204} {
		p, sink := start(t, withRatio(0), seeded())
		in := logRecord(shapes[0], status, "/healthz", "")
		want := marshal(t, in)

		consume(t, p, in)

		if len(sink.AllLogs()) != 1 || marshal(t, sink.AllLogs()[0]) != want {
			t.Errorf("status %d was sampled", status)
		}
	}
}

func TestRatioZero_DropsEveryProbe_RatioOneKeepsEvery(t *testing.T) {
	for ratio, want := range map[float64]int{0: 0, 1: 50} {
		p, sink := start(t, withRatio(ratio), seeded())
		ld := plog.NewLogs()
		sl := ld.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty()
		for i := 0; i < 50; i++ {
			logRecord(shapes[0], 200, "/healthz", "").ResourceLogs().At(0).ScopeLogs().At(0).LogRecords().At(0).CopyTo(sl.LogRecords().AppendEmpty())
		}

		consume(t, p, ld)

		if got := sink.LogRecordCount(); got != want {
			t.Errorf("ratio %v kept %d of 50, want %d", ratio, got, want)
		}
	}
}

func TestSeededSource_10000Probes_Keeps1800To2200AtPointTwo(t *testing.T) {
	p, sink := start(t, withRatio(0.2), seeded())
	ld := plog.NewLogs()
	sl := ld.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty()
	template := logRecord(shapes[0], 200, "/healthz", "").ResourceLogs().At(0).ScopeLogs().At(0).LogRecords().At(0)
	for i := 0; i < 10000; i++ {
		template.CopyTo(sl.LogRecords().AppendEmpty())
	}

	consume(t, p, ld)

	if got := sink.LogRecordCount(); got < 1800 || got > 2200 {
		t.Errorf("kept %d of 10000, want 1800-2200", got)
	}
}

func TestKeptProbe_OriginalAndFullMessageUntouched(t *testing.T) {
	for _, s := range shapes {
		t.Run(s.name, func(t *testing.T) {
			p, sink := start(t, withRatio(1), seeded())

			consume(t, p, logRecord(s, 200, "/healthz", ""))

			a := onlyRecord(t, sink).Attributes()
			if v, _ := a.Get("original_message"); v.Str() != "raw line for /healthz" {
				t.Errorf("original_message = %q", v.Str())
			}
			if v, _ := a.Get("full_message"); v.Str() != "full line for /healthz" {
				t.Errorf("full_message = %q", v.Str())
			}
		})
	}
}

func TestSamplingOff_OutputEqualsInput(t *testing.T) {
	cfg := withRatio(0)
	cfg.Enabled = false
	p, sink := start(t, cfg, seeded())
	in := logRecord(shapes[0], 200, "/healthz", "kube-probe/1.29")
	want := marshal(t, in)

	consume(t, p, in)

	if len(sink.AllLogs()) != 1 || marshal(t, sink.AllLogs()[0]) != want {
		t.Error("record changed with sampling off")
	}
}

func TestRecordWithoutProbeAttributes_PassesUnchanged(t *testing.T) {
	p, sink := start(t, withRatio(0), seeded())
	ld := plog.NewLogs()
	ld.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty().LogRecords().AppendEmpty().Body().SetStr("plain")
	want := marshal(t, ld)

	consume(t, p, ld)

	if len(sink.AllLogs()) != 1 || marshal(t, sink.AllLogs()[0]) != want {
		t.Error("record without attributes changed")
	}
}

func TestUnparsableStatus_PassesUnchanged(t *testing.T) {
	p, sink := start(t, withRatio(0), seeded())
	ld := logRecord(shapes[1], 200, "/healthz", "")
	ld.ResourceLogs().At(0).ScopeLogs().At(0).LogRecords().At(0).Attributes().PutStr("http.response.status_code", "abc")
	want := marshal(t, ld)

	consume(t, p, ld)

	if len(sink.AllLogs()) != 1 || marshal(t, sink.AllLogs()[0]) != want {
		t.Error("record with a non-numeric status changed")
	}
}

func TestCustomAttributeNames_AreRead(t *testing.T) {
	cfg := withRatio(0)
	cfg.StatusAttribute, cfg.PathAttribute, cfg.AgentAttribute = "http.status", "req.path", "req.agent"
	p, sink := start(t, cfg, seeded())
	ld := plog.NewLogs()
	a := ld.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty().LogRecords().AppendEmpty().Attributes()
	a.PutInt("http.status", 200)
	a.PutStr("req.path", "/healthz")

	consume(t, p, ld)

	if sink.LogRecordCount() != 0 {
		t.Error("probe under custom attribute names was not dropped")
	}
}

func TestMixedBatch_DropsOnlyProbesAndPrunesEmptyContainers(t *testing.T) {
	p, sink := start(t, withRatio(0), seeded())
	ld := plog.NewLogs()
	probeRL := ld.ResourceLogs().AppendEmpty()
	logRecord(shapes[0], 200, "/healthz", "").ResourceLogs().At(0).ScopeLogs().At(0).LogRecords().At(0).CopyTo(probeRL.ScopeLogs().AppendEmpty().LogRecords().AppendEmpty())
	keepRL := ld.ResourceLogs().AppendEmpty()
	logRecord(shapes[0], 200, "/orders", "").ResourceLogs().At(0).ScopeLogs().At(0).LogRecords().At(0).CopyTo(keepRL.ScopeLogs().AppendEmpty().LogRecords().AppendEmpty())

	consume(t, p, ld)

	out := sink.AllLogs()[0]
	if out.ResourceLogs().Len() != 1 || out.LogRecordCount() != 1 {
		t.Fatalf("got %d ResourceLogs and %d records, want 1 and 1", out.ResourceLogs().Len(), out.LogRecordCount())
	}
}

func TestCounters_CountKeptAndDropped(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	set := processortest.NewNopSettings(component.MustNewType(typeStr))
	set.MeterProvider = sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	sink := new(consumertest.LogsSink)
	draws := []float64{0.0, 0.9, 0.9}
	i := 0
	p, err := newProcessor(withRatio(0.5), set.TelemetrySettings, sink, func() float64 { v := draws[i]; i++; return v })
	if err != nil {
		t.Fatalf("newProcessor: %v", err)
	}
	ld := plog.NewLogs()
	sl := ld.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty()
	template := logRecord(shapes[0], 200, "/healthz", "").ResourceLogs().At(0).ScopeLogs().At(0).LogRecords().At(0)
	for n := 0; n < 3; n++ {
		template.CopyTo(sl.LogRecords().AppendEmpty())
	}

	consume(t, p, ld)

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}
	got := map[string]int64{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if sum, ok := m.Data.(metricdata.Sum[int64]); ok && len(sum.DataPoints) == 1 {
				got[m.Name] = sum.DataPoints[0].Value
			}
		}
	}
	if got["healthprobe.kept"] != 1 || got["healthprobe.dropped"] != 2 {
		t.Errorf("counters = %v, want kept 1 dropped 2", got)
	}
}

func TestConfigValidate(t *testing.T) {
	cases := map[string]func(*Config){
		"bad ratio":        func(c *Config) { c.KeepRatio = 2 },
		"empty status key": func(c *Config) { c.StatusAttribute = "" },
		"empty path key":   func(c *Config) { c.PathAttribute = "" },
		"empty agent key":  func(c *Config) { c.AgentAttribute = "" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := withRatio(0.2)
			mutate(cfg)
			if err := cfg.Validate(); err == nil {
				t.Error("expected a validation error")
			}
		})
	}
}

func TestDefaultConfig_IsEnabledWithStandardAttributeNames(t *testing.T) {
	cfg := withRatio(0.2)

	if !cfg.Enabled || cfg.StatusAttribute != "http.response.status_code" ||
		cfg.PathAttribute != "url.path" || cfg.AgentAttribute != "user_agent.original" {
		t.Errorf("default config = %+v", cfg)
	}
}
