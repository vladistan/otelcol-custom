// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"maps"
	"testing"
	"time"

	"go.opentelemetry.io/collector/pdata/plog"
)

// runScoped sends one record and returns it with the scope name it left with.
func (h *harness) runScoped(in input) (plog.LogRecord, string) {
	h.t.Helper()
	lr, _ := h.run(in)
	all := h.sink.AllLogs()
	return lr, all[len(all)-1].ResourceLogs().At(0).ScopeLogs().At(0).Scope().Name()
}

// onlyHarness enables one branch, so a test sees that branch and no other.
func onlyHarness(t *testing.T, name string) *harness {
	t.Helper()
	cfg := defaultConfig()
	cfg.Branches = []string{name}
	return newHarness(t, cfg)
}

func wantSeverity(t *testing.T, lr plog.LogRecord, num plog.SeverityNumber, text string) {
	t.Helper()
	if lr.SeverityNumber() != num || lr.SeverityText() != text {
		t.Fatalf("severity = %d %q, want %d %q", lr.SeverityNumber(), lr.SeverityText(), num, text)
	}
}

func wantBody(t *testing.T, lr plog.LogRecord, want string) {
	t.Helper()
	if got := lr.Body().Str(); got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
}

func wantTime(t *testing.T, lr plog.LogRecord, want time.Time) {
	t.Helper()
	if !lr.Timestamp().AsTime().Equal(want) {
		t.Fatalf("time = %v, want %v", lr.Timestamp().AsTime(), want)
	}
}

func wantScope(t *testing.T, got, want string) {
	t.Helper()
	if got != want {
		t.Fatalf("scope name = %q, want %q", got, want)
	}
}

// wantUnchanged checks that a record the branch could not shape kept its body and attributes.
func wantUnchanged(t *testing.T, lr plog.LogRecord, body string, attrs map[string]any) {
	t.Helper()
	if lr.Body().Str() != body || !maps.Equal(lr.Attributes().AsRaw(), attrs) || lr.SeverityNumber() != plog.SeverityNumberUnspecified {
		t.Fatalf("record changed: body %q attrs %v severity %d", lr.Body().Str(), lr.Attributes().AsRaw(), lr.SeverityNumber())
	}
}

func wantFailed(t *testing.T, h *harness, format string) {
	t.Helper()
	if got := h.counter(parseFailedMetric, "format"); !maps.Equal(got, map[string]int64{format: 1}) {
		t.Fatalf("parse_failed = %v, want %s once", got, format)
	}
}
