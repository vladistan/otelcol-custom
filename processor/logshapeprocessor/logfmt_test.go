// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"testing"
	"time"

	"go.opentelemetry.io/collector/pdata/plog"
)

// Invented fixtures: shapes of logfmt lines from the ledger, not captures. A dashboard line with a
// logger, a source location and a user, and a time-series database line with ts= and lvl=.
const (
	logfmtDashboard = `logger=plugin.x t=2026-11-19T22:42:20.918289Z level=info msg="performing QueryData" dsName=a url=/b source=write_handler.go:254 uname=user@example.com empty=`
	logfmtInflux    = `ts=2026-11-19T23:00:00.000000Z lvl=info msg="Executing query" log_id=0abc`
)

func TestLogfmt_DashboardLine_GivesTheMessageWithItsPairsAndTheScope(t *testing.T) {
	lr, res := runW4(t, input{body: logfmtDashboard})

	w4Body(t, lr, "performing QueryData dsName=a url=/b")
	w4Severity(t, lr, plog.SeverityNumberInfo, "INFO")
	w4Time(t, lr, time.Date(2026, 11, 19, 22, 42, 20, 918289000, time.UTC))
	a := lr.Attributes()
	if attrStr(t, a, "code.file.path") != "write_handler.go" || attrStr(t, a, "user.email") != "user@example.com" || attrStr(t, a, "log.record.original") != logfmtDashboard {
		t.Fatalf("attributes = %v resource = %v", a.AsRaw(), res.Attributes().AsRaw())
	}
	attrInt(t, a, "code.line.number", 254)
	absent(t, a, "uname", "source", "empty", "logger")
}

func TestLogfmt_DashboardLine_PutsTheLoggerInTheScopeName(t *testing.T) {
	lr, scope := runScopedW4(t, input{body: logfmtDashboard})

	w4Scope(t, scope, "plugin.x")
	_ = lr
}

func TestLogfmt_InfluxLine_UsesTsAndLvl(t *testing.T) {
	lr, _ := runW4(t, input{body: logfmtInflux})

	w4Body(t, lr, "Executing query log_id=0abc")
	w4Severity(t, lr, plog.SeverityNumberInfo, "INFO")
	w4Time(t, lr, time.Date(2026, 11, 19, 23, 0, 0, 0, time.UTC))
}

func TestLogfmt_ComponentAndModuleAreScopeNamesToo(t *testing.T) {
	for key, want := range map[string]string{"component": "dns", "module": "tls"} {
		line := `t=2026-11-19T22:42:20Z level=warn msg="slow" ` + key + `=` + want
		lr, scope := runScopedW4(t, input{body: line})

		w4Scope(t, scope, want)
		w4Severity(t, lr, plog.SeverityNumberWarn, "WARN")
	}
}

// INVENTED fixtures: the shapes of a Grafana line with a +0100 offset and a Prometheus line with caller=.
func TestLogfmt_TimeWithAZoneOffsetWithoutAColon_IsRead(t *testing.T) {
	lr, _ := runW4(t, input{body: `logger=a t=2026-11-19T23:42:20.918+0100 level=info msg="x"`})

	w4Time(t, lr, time.Date(2026, 11, 19, 22, 42, 20, 918000000, time.UTC))
	w4Body(t, lr, "x")
}

func TestLogfmt_TimeWithAZoneOffsetAndAColon_IsRead(t *testing.T) {
	lr, _ := runW4(t, input{body: `logger=a t=2026-11-19T23:42:20+01:00 level=info msg="x"`})

	w4Time(t, lr, time.Date(2026, 11, 19, 22, 42, 20, 0, time.UTC))
}

func TestLogfmt_CallerPair_BecomesTheSourceLocationAndLeavesTheBody(t *testing.T) {
	lr, _ := runW4(t, input{body: `ts=2026-11-19T23:00:00.000Z caller=main.go:123 level=info component=web msg="Start listening"`})

	w4Body(t, lr, "Start listening")
	a := lr.Attributes()
	attrInt(t, a, "code.line.number", 123)
	if attrStr(t, a, "code.file.path") != "main.go" {
		t.Fatalf("attributes = %v", a.AsRaw())
	}
}

func TestLogfmt_UnknownLevel_LeavesTheSeverityUnset(t *testing.T) {
	lr, _ := runW4(t, input{body: `logger=a t=2026-11-19T22:42:20Z level=chatty msg="hello"`})

	w4Body(t, lr, "hello")
	w4Severity(t, lr, plog.SeverityNumberUnspecified, "")
}

func TestLogfmt_LineWithATimeKey_StaysWithTheLogrusBranch(t *testing.T) {
	cfg := defaultConfig()
	cfg.Branches = []string{"logfmt"}
	const line = `time="2025-12-31T04:25:29Z" level=warning msg="slow sync" duration=3s`

	lr, _ := newHarness(t, cfg).run(input{body: line})

	w4Untouched(t, lr, line, map[string]any{})
}

func TestLogfmt_BadTime_IsUnchangedAndCounted(t *testing.T) {
	h := newHarness(t, defaultConfig())
	const bad = `logger=a t=yesterday level=info msg="x"`

	lr, _ := h.run(input{body: bad})

	w4Untouched(t, lr, bad, map[string]any{})
	w4Failed(t, h, "logfmt")
}
