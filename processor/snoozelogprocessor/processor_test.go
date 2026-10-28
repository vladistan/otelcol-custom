// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package snoozelogprocessor

import (
	"context"
	"testing"
	"time"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/processor/processortest"
)

func defaultConfig() *Config {
	return NewFactory().CreateDefaultConfig().(*Config)
}

// record builds one batch the way otel-node-logs delivers a container line:
// namespace and container as resource attributes, the line as a string body.
func record(namespace, container string, body pcommon.Value) (plog.Logs, plog.LogRecord) {
	ld := plog.NewLogs()
	rl := ld.ResourceLogs().AppendEmpty()
	rl.Resource().Attributes().PutStr("k8s.namespace.name", namespace)
	rl.Resource().Attributes().PutStr("k8s.container.name", container)
	lr := rl.ScopeLogs().AppendEmpty().LogRecords().AppendEmpty()
	body.CopyTo(lr.Body())
	return ld, lr
}

func strBody(s string) pcommon.Value {
	v := pcommon.NewValueEmpty()
	v.SetStr(s)
	return v
}

func run(t *testing.T, cfg *Config, ld plog.Logs) plog.LogRecord {
	t.Helper()
	sink := new(consumertest.LogsSink)
	set := processortest.NewNopSettings(component.MustNewType(typeStr))
	proc, err := NewFactory().CreateLogs(context.Background(), set, cfg, sink)
	if err != nil {
		t.Fatalf("create processor: %v", err)
	}
	if err := proc.ConsumeLogs(context.Background(), ld); err != nil {
		t.Fatalf("consume: %v", err)
	}
	if sink.LogRecordCount() != 1 {
		t.Fatalf("sink saw %d records, want 1", sink.LogRecordCount())
	}
	return sink.AllLogs()[0].ResourceLogs().At(0).ScopeLogs().At(0).LogRecords().At(0)
}

func TestLevels_MapPerD7(t *testing.T) {
	cases := []struct {
		level  string
		number plog.SeverityNumber
		text   string
	}{
		{"DEBUG", plog.SeverityNumberDebug, "DEBUG"},
		{"INFO", plog.SeverityNumberInfo, "INFO"},
		{"WARNING", plog.SeverityNumberWarn, "WARN"},
		{"WARN", plog.SeverityNumberWarn, "WARN"},
		{"ERROR", plog.SeverityNumberError, "ERROR"},
		{"CRITICAL", plog.SeverityNumberFatal, "FATAL"},
		{"FATAL", plog.SeverityNumberFatal, "FATAL"},
	}
	for _, tc := range cases {
		t.Run(tc.level, func(t *testing.T) {
			ld, _ := record("anemo", "snooze-processor", strBody("12:34:56 "+tc.level+" job finished"))

			got := run(t, defaultConfig(), ld)

			if got.SeverityNumber() != tc.number || got.SeverityText() != tc.text {
				t.Errorf("severity = %v/%q, want %v/%q", got.SeverityNumber(), got.SeverityText(), tc.number, tc.text)
			}
		})
	}
}

func TestMatchingLine_BodyBecomesMessage(t *testing.T) {
	ld, _ := record("anemo", "snooze-processor", strBody("12:34:56 INFO snoozed 3 alerts"))

	got := run(t, defaultConfig(), ld)

	if got.Body().Str() != "snoozed 3 alerts" {
		t.Errorf("body = %q, want %q", got.Body().Str(), "snoozed 3 alerts")
	}
}

func TestMatchingLine_TimestampUnchanged(t *testing.T) {
	ld, lr := record("anemo", "snooze-processor", strBody("12:34:56 ERROR failed"))
	want := pcommon.NewTimestampFromTime(time.Date(2026, 10, 27, 3, 4, 5, 0, time.UTC))
	lr.SetTimestamp(want)
	lr.SetObservedTimestamp(want + 1)

	got := run(t, defaultConfig(), ld)

	if got.Timestamp() != want || got.ObservedTimestamp() != want+1 {
		t.Errorf("timestamps changed: %v / %v", got.Timestamp(), got.ObservedTimestamp())
	}
}

func assertUnchanged(t *testing.T, got plog.LogRecord, body string) {
	t.Helper()
	if got.Body().Str() != body {
		t.Errorf("body = %q, want unchanged %q", got.Body().Str(), body)
	}
	if got.SeverityNumber() != plog.SeverityNumberUnspecified || got.SeverityText() != "" {
		t.Errorf("severity = %v/%q, want unchanged", got.SeverityNumber(), got.SeverityText())
	}
}

func TestNonMatchingLine_PassesThroughUnchanged(t *testing.T) {
	line := "starting snooze processor"
	ld, _ := record("anemo", "snooze-processor", strBody(line))

	assertUnchanged(t, run(t, defaultConfig(), ld), line)
}

func TestMatchingLine_FromOtherContainer_PassesThroughUnchanged(t *testing.T) {
	line := "12:34:56 INFO job finished"
	ld, _ := record("anemo", "other-app", strBody(line))

	assertUnchanged(t, run(t, defaultConfig(), ld), line)
}

func TestMatchingLine_FromOtherNamespace_PassesThroughUnchanged(t *testing.T) {
	line := "12:34:56 INFO job finished"
	ld, _ := record("default", "snooze-processor", strBody(line))

	assertUnchanged(t, run(t, defaultConfig(), ld), line)
}

func TestContainerNameIsExact_NotSubstring(t *testing.T) {
	line := "12:34:56 INFO job finished"
	ld, _ := record("anemo", "snooze-processor-sidecar", strBody(line))

	assertUnchanged(t, run(t, defaultConfig(), ld), line)
}

func TestUnknownLevel_PassesThroughUnchanged(t *testing.T) {
	for _, level := range []string{"TRACE", "NOTICE", "info"} {
		t.Run(level, func(t *testing.T) {
			line := "12:34:56 " + level + " something"
			ld, _ := record("anemo", "snooze-processor", strBody(line))

			assertUnchanged(t, run(t, defaultConfig(), ld), line)
		})
	}
}

func TestMultiLine_OnlyFirstLineIsMatched(t *testing.T) {
	ld, _ := record("anemo", "snooze-processor", strBody("12:34:56 ERROR boom\n12:34:57 INFO trailing"))

	got := run(t, defaultConfig(), ld)

	if got.SeverityNumber() != plog.SeverityNumberError {
		t.Errorf("severity = %v, want ERROR from the first line", got.SeverityNumber())
	}
	if want := "boom\n12:34:57 INFO trailing"; got.Body().Str() != want {
		t.Errorf("body = %q, want %q", got.Body().Str(), want)
	}
}

func TestMultiLine_MatchOnLaterLineIsIgnored(t *testing.T) {
	line := "starting\n12:34:56 ERROR boom"
	ld, _ := record("anemo", "snooze-processor", strBody(line))

	assertUnchanged(t, run(t, defaultConfig(), ld), line)
}

func TestNonStringBody_PassesThroughUnchanged(t *testing.T) {
	body := pcommon.NewValueMap()
	body.Map().PutStr("text", "12:34:56 INFO job finished")
	ld, _ := record("anemo", "snooze-processor", body)

	got := run(t, defaultConfig(), ld)

	if got.Body().Type() != pcommon.ValueTypeMap || got.SeverityNumber() != plog.SeverityNumberUnspecified {
		t.Errorf("map body record changed: type=%v severity=%v", got.Body().Type(), got.SeverityNumber())
	}
}

func TestConfiguredMatchList_ReplacesDefault(t *testing.T) {
	cfg := &Config{Match: []MatchRule{{Namespace: "batch", Container: "nightly"}}}
	line := "01:02:03 WARN slow"

	ld, _ := record("batch", "nightly", strBody(line))
	got := run(t, cfg, ld)
	if got.SeverityNumber() != plog.SeverityNumberWarn || got.Body().Str() != "slow" {
		t.Errorf("configured rule did not match: %v %q", got.SeverityNumber(), got.Body().Str())
	}

	ld, _ = record("anemo", "snooze-processor", strBody(line))
	assertUnchanged(t, run(t, cfg, ld), line)
}

func TestDefaultConfig_MatchesAnemoSnoozeProcessor(t *testing.T) {
	cfg := defaultConfig()

	if len(cfg.Match) != 1 || cfg.Match[0] != (MatchRule{Namespace: "anemo", Container: "snooze-processor"}) {
		t.Errorf("default match = %+v, want anemo/snooze-processor", cfg.Match)
	}
}

func TestConfigValidate_RejectsEmptyOrIncompleteRules(t *testing.T) {
	cases := map[string]*Config{
		"no rules":          {},
		"empty namespace":   {Match: []MatchRule{{Container: "c"}}},
		"empty container":   {Match: []MatchRule{{Namespace: "n"}}},
		"second rule empty": {Match: []MatchRule{{Namespace: "n", Container: "c"}, {}}},
	}
	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			if err := cfg.Validate(); err == nil {
				t.Error("expected a validation error")
			}
		})
	}
}
