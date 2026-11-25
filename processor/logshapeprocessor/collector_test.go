// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"maps"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/collector/pdata/plog"
)

// zapLine joins the fields of a collector console line with tabs.
func zapLine(parts ...string) string { return strings.Join(parts, "\t") }

const resourceBlock = `"resource": {"service.instance.id": "1a7177a9-ec8b-4c46-92be-797bdd0d7bb7", "service.name": "otelcol-gateway", "service.version": "0.3.6"}`

var (
	// Shapes of the own log of a gateway run (edge timestamp form, both caller forms); the values are edited, not a capture.
	collectorEdge = zapLine("2026-11-06T16:12:29.299-0500", "info", "otlpreceiver@v0.164.0/otlp.go:175", "Starting HTTP server",
		`{`+resourceBlock+`, "otelcol.component.id": "otlp", "otelcol.component.kind": "receiver", "endpoint": "127.0.0.1:14418"}`)
	collectorStandard = zapLine("2026-11-06T21:12:29.299Z", "info", "otlpreceiver@v0.164.0/otlp.go:175", "Starting HTTP server",
		`{"otelcol.component.id": "otlp", "otelcol.component.kind": "receiver", "endpoint": "127.0.0.1:14418"}`)
	collectorBare = zapLine("2026-11-06T16:12:29.298-0500", "info", "builders/builders.go:28", "Development component. May change in the future.",
		`{`+resourceBlock+`, "otelcol.component.id": "identityfill", "otelcol.component.kind": "processor", "otelcol.pipeline.id": "logs", "otelcol.signal": "logs"}`)
	collectorWrapped = "otelcol-contrib[1186]: " + strings.ReplaceAll(collectorStandard, "\t", "#011")
	collectorRetry   = zapLine("2026-11-19T17:45:32.568-0500", "info", "exporterhelper@v0.164.0/retry_sender.go:107", "Exporting failed. Will retry the request after interval.",
		`{"otelcol.component.id": "otlp_http", "otelcol.component.kind": "exporter", "otelcol.signal": "logs", "error": "failed to make an HTTP request: connection refused", "interval": "5.2s"}`)
	collectorDebug = zapLine("2026-11-19T17:45:33.100-0500", "info", "debugexporter@v0.164.0/exporter.go:60", "Metrics",
		`{`+resourceBlock+`, "otelcol.component.id": "debug", "otelcol.component.kind": "exporter", "otelcol.signal": "metrics", "resource metrics": 1, "metrics": 40, "data points": 93}`)
	nodeLogsTail = `{"otelcol.component.id": "debug", "otelcol.component.kind": "exporter", "otelcol.signal": "metrics", "resource metrics": 1, "metrics": 40, "data points": 93}`
)

var (
	standardTime = time.Date(2026, 11, 6, 21, 12, 29, 299000000, time.UTC)
	edgeRetryAt  = time.Date(2026, 11, 19, 22, 45, 32, 568000000, time.UTC)
)

func TestCollector_StandardLine_GivesTheMessageAndTheFields(t *testing.T) {
	h := newHarness(t, defaultConfig())

	lr, scope := h.runScoped(input{body: collectorStandard})

	wantBody(t, lr, "Starting HTTP server")
	wantSeverity(t, lr, plog.SeverityNumberInfo, "INFO")
	wantTime(t, lr, standardTime)
	wantScope(t, scope, "otlp")
	a := lr.Attributes()
	if attrStr(t, a, "otel.component.name") != "otlp" || attrStr(t, a, "vstan.collector.endpoint") != "127.0.0.1:14418" {
		t.Fatalf("attributes = %v", a.AsRaw())
	}
	absent(t, a, "endpoint")
	if attrStr(t, a, "code.file.path") != "otlpreceiver/otlp.go" || attrStr(t, a, "log.record.original") != collectorStandard {
		t.Fatalf("attributes = %v", a.AsRaw())
	}
	attrInt(t, a, "code.line.number", 175)
	absent(t, a, "otelcol.component.id", "otelcol.component.kind", "otelcol.signal", "resource")
}

func TestCollector_EdgeTimestampAndResourceBlock_GiveTheSameShape(t *testing.T) {
	h := newHarness(t, defaultConfig())

	lr, scope := h.runScoped(input{body: collectorEdge})

	wantBody(t, lr, "Starting HTTP server")
	wantTime(t, lr, standardTime)
	wantScope(t, scope, "otlp")
	absent(t, lr.Attributes(), "resource", "otelcol.component.kind", "service.name")
}

func TestCollector_StandardEdgeAndWrappedForms_GiveTheSameRecord(t *testing.T) {
	const fields = `"otelcol.component.id": "filelog", "otelcol.component.kind": "receiver", "path": "/var/log/app.log"`
	forms := map[string]string{
		"standard": zapLine("2026-11-19T22:45:32.568Z", "info", "fileconsumer@v0.164.0/file.go:470", "Started watching file", `{`+fields+`}`),
		"edge":     zapLine("2026-11-19T17:45:32.568-0500", "info", "fileconsumer/file.go:470", "Started watching file", `{`+resourceBlock+`, `+fields+`}`),
		"wrapped":  "otelcol-contrib[1186]: " + strings.ReplaceAll(zapLine("2026-11-19T22:45:32.568Z", "info", "fileconsumer@v0.164.0/file.go:470", "Started watching file", `{`+fields+`}`), "\t", "#011"),
	}
	type shape struct {
		body, severity, scope string
		attrs                 map[string]any
		at                    time.Time
	}
	got := map[string]shape{}
	for name, line := range forms {
		lr, scope := newHarness(t, defaultConfig()).runScoped(input{body: line})
		attrs := lr.Attributes().AsRaw()
		delete(attrs, "log.record.original")
		delete(attrs, "process.pid")
		got[name] = shape{lr.Body().Str(), lr.SeverityText(), scope, attrs, lr.Timestamp().AsTime()}
	}
	want := got["standard"]
	if want.attrs["code.file.path"] != "fileconsumer/file.go" {
		t.Fatalf("code.file.path = %v, want fileconsumer/file.go", want.attrs["code.file.path"])
	}
	for name, s := range got {
		if s.body != want.body || s.severity != want.severity || s.scope != want.scope || !s.at.Equal(want.at) || !maps.Equal(s.attrs, want.attrs) {
			t.Errorf("%s form = %+v, want %+v", name, s, want)
		}
	}
}

func TestCollector_CallerWithoutNameAndVersion_KeepsThePath(t *testing.T) {
	h := newHarness(t, defaultConfig())

	lr, scope := h.runScoped(input{body: collectorBare})

	wantBody(t, lr, "Development component. May change in the future.")
	wantScope(t, scope, "identityfill")
	a := lr.Attributes()
	if attrStr(t, a, "code.file.path") != "builders/builders.go" || attrStr(t, a, "otelcol.pipeline.id") != "logs" {
		t.Fatalf("attributes = %v", a.AsRaw())
	}
	attrInt(t, a, "code.line.number", 28)
	absent(t, a, "otelcol.signal", "otelcol.component.kind")
}

func TestCollector_SyslogWrappedLine_GivesTheSameShapeAndThePid(t *testing.T) {
	h := newHarness(t, defaultConfig())

	lr, scope := h.runScoped(input{body: collectorWrapped})

	wantBody(t, lr, "Starting HTTP server")
	wantSeverity(t, lr, plog.SeverityNumberInfo, "INFO")
	wantTime(t, lr, standardTime)
	wantScope(t, scope, "otlp")
	attrInt(t, lr.Attributes(), "process.pid", 1186)
	if attrStr(t, lr.Attributes(), "vstan.collector.endpoint") != "127.0.0.1:14418" {
		t.Fatalf("attributes = %v", lr.Attributes().AsRaw())
	}
}

func TestCollector_RetryLine_StaysInfoAndKeepsTheError(t *testing.T) {
	h := newHarness(t, defaultConfig())

	lr, _ := h.runScoped(input{body: collectorRetry})

	wantBody(t, lr, "Exporting failed. Will retry the request after interval.")
	wantSeverity(t, lr, plog.SeverityNumberInfo, "INFO")
	wantTime(t, lr, edgeRetryAt)
	if attrStr(t, lr.Attributes(), "exception.message") != "failed to make an HTTP request: connection refused" || attrStr(t, lr.Attributes(), "vstan.collector.interval") != "5.2s" {
		t.Fatalf("attributes = %v", lr.Attributes().AsRaw())
	}
	absent(t, lr.Attributes(), "error", "interval")
}

func TestCollector_DebugExporterLine_IsDebugWithTheCountsInTheBody(t *testing.T) {
	h := newHarness(t, defaultConfig())

	lr, scope := h.runScoped(input{body: collectorDebug})

	wantBody(t, lr, "Metrics resource metrics=1 metrics=40 data points=93")
	wantSeverity(t, lr, plog.SeverityNumberDebug, "DEBUG")
	wantScope(t, scope, "debug")
	absent(t, lr.Attributes(), "data points", "metrics", "resource metrics")
}

func TestCollector_NodeLogsRowWithAJsonTailBody_GetsTheDebugBody(t *testing.T) {
	h := newHarness(t, defaultConfig())

	lr, _ := h.runScoped(input{
		body:  nodeLogsTail,
		attrs: map[string]any{"original_message": collectorDebug, "otelcol.component": "debug", "processed": true},
	})

	wantBody(t, lr, "Metrics resource metrics=1 metrics=40 data points=93")
	wantSeverity(t, lr, plog.SeverityNumberDebug, "DEBUG")
	a := lr.Attributes()
	if attrStr(t, a, "log.record.original") != collectorDebug || attrStr(t, a, "otel.component.name") != "debug" {
		t.Fatalf("attributes = %v", a.AsRaw())
	}
	absent(t, a, "original_message", "otelcol.component")
	if v, ok := a.Get("processed"); !ok || !v.Bool() {
		t.Fatalf("processed lost: %v", a.AsRaw())
	}
}

func TestCollector_LineWithOnlyTheComponent_GivesTheMessageAndTheScope(t *testing.T) {
	h := newHarness(t, defaultConfig())

	lr, scope := h.runScoped(input{body: zapLine("2026-11-19T17:45:32.568-0500", "warn", "service@v0.164.0/service.go:300", "Everything is ready.", `{"otelcol.component.id": "service"}`)})

	wantBody(t, lr, "Everything is ready.")
	wantSeverity(t, lr, plog.SeverityNumberWarn, "WARN")
	wantScope(t, scope, "service")
}

func TestCollector_ZapConsoleLineOfAnotherProgram_IsNotClaimed(t *testing.T) {
	cfg := defaultConfig()
	cfg.Branches = []string{"collector"}
	h := newHarness(t, cfg)
	line := zapLine("2026-11-19T17:45:32.568-0500", "warn", "internal/retry_sender.go:133", "Exporting failed.", `{"attempt":2}`)

	lr, _ := h.runScoped(input{body: line})

	wantUnchanged(t, lr, line, map[string]any{})
}

func TestCollector_UnknownLevel_LeavesTheSeverityUnset(t *testing.T) {
	h := newHarness(t, defaultConfig())

	lr, _ := h.runScoped(input{body: zapLine("2026-11-19T17:45:32.568-0500", "chatty", "a/b.go:1", "hello", `{"otelcol.component.id": "x"}`)})

	wantBody(t, lr, "hello")
	wantSeverity(t, lr, plog.SeverityNumberUnspecified, "")
}

func TestCollector_ClaimedLineWithABadTimestamp_IsUnchangedAndCounted(t *testing.T) {
	h := newHarness(t, defaultConfig())
	bad := zapLine("2026-13-45T25:61:00.000Z", "info", "a/b.go:1", "hello", `{"otelcol.component.id": "x"}`)

	lr, _ := h.runScoped(input{body: bad})

	wantUnchanged(t, lr, bad, map[string]any{})
	wantFailed(t, h, "collector")
}

func TestCollector_BrokenJsonTail_IsUnchangedAndCounted(t *testing.T) {
	h := newHarness(t, defaultConfig())
	bad := zapLine("2026-11-19T17:45:32.568-0500", "info", "a/b.go:1", "hello", `{"otelcol.component.id": `)

	lr, _ := h.runScoped(input{body: bad})

	wantUnchanged(t, lr, bad, map[string]any{})
	wantFailed(t, h, "collector")
}

func TestCollector_TextWithTabsThatIsNotACollectorLine_IsNotClaimed(t *testing.T) {
	h := newHarness(t, defaultConfig())
	line := zapLine("date", "time", "x")

	lr, _ := h.runScoped(input{body: line})

	wantUnchanged(t, lr, line, map[string]any{})
	if got := h.counter(parseFailedMetric, "format"); len(got) != 0 {
		t.Fatalf("parse_failed = %v, want none", got)
	}
}
