// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"maps"
	"testing"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
)

// attributeKeyed are the branches that claim by an attribute (a task file name, a cloud resource id,
// the openvpnstatus counters), so a body-only sample file cannot reach them; their tests send the attributes.
var attributeKeyed = map[string]bool{"openvpn": true, "pbs": true, "pve": true, "serialconsole": true, "stderrhint": true}

// runW4 sends one record and returns it with its resource.
func runW4(t *testing.T, in input) (plog.LogRecord, pcommon.Resource) {
	t.Helper()
	return newHarness(t, defaultConfig()).run(in)
}

// runScopedW4 sends one record and returns it with the scope name it left with.
func runScopedW4(t *testing.T, in input) (plog.LogRecord, string) {
	t.Helper()
	h := newHarness(t, defaultConfig())
	lr, _ := h.run(in)
	all := h.sink.AllLogs()
	return lr, all[len(all)-1].ResourceLogs().At(0).ScopeLogs().At(0).Scope().Name()
}

func w4Scope(t *testing.T, got, want string) {
	t.Helper()
	if got != want {
		t.Fatalf("scope name = %q, want %q", got, want)
	}
}

func w4Body(t *testing.T, lr plog.LogRecord, want string) {
	t.Helper()
	if got := lr.Body().Str(); got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
}

func w4Severity(t *testing.T, lr plog.LogRecord, num plog.SeverityNumber, text string) {
	t.Helper()
	if lr.SeverityNumber() != num || lr.SeverityText() != text {
		t.Fatalf("severity = %d %q, want %d %q", lr.SeverityNumber(), lr.SeverityText(), num, text)
	}
}

func w4Time(t *testing.T, lr plog.LogRecord, want time.Time) {
	t.Helper()
	if !lr.Timestamp().AsTime().Equal(want) {
		t.Fatalf("time = %v, want %v", lr.Timestamp().AsTime(), want)
	}
}

// w4Untouched checks a record no branch of this wave may change.
func w4Untouched(t *testing.T, lr plog.LogRecord, body string, attrs map[string]any) {
	t.Helper()
	if lr.Body().Str() != body || !maps.Equal(lr.Attributes().AsRaw(), attrs) || lr.SeverityNumber() != plog.SeverityNumberUnspecified {
		t.Fatalf("record changed: body %q attrs %v severity %d", lr.Body().Str(), lr.Attributes().AsRaw(), lr.SeverityNumber())
	}
}

func w4Failed(t *testing.T, h *harness, format string) {
	t.Helper()
	if got := h.counter(parseFailedMetric, "format"); !maps.Equal(got, map[string]int64{format: 1}) {
		t.Fatalf("parse_failed = %v, want %s once", got, format)
	}
}
