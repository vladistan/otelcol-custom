// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"maps"
	"testing"
	"time"

	"go.opentelemetry.io/collector/pdata/plog"
)

func TestLogrus_JSONLine_GivesTheMessageTheLevelAndTheTime(t *testing.T) {
	h := newHarness(t, defaultConfig())
	const line = `{"level":"info","msg":"All records are up to date","time":"2025-12-31T04:03:58Z"}`

	lr, _ := h.run(input{body: line, ts: recordTime})

	if lr.Body().Str() != "All records are up to date" {
		t.Errorf("body = %q", lr.Body().Str())
	}
	if lr.SeverityNumber() != plog.SeverityNumberInfo || lr.SeverityText() != "INFO" {
		t.Errorf("severity = %d %q", lr.SeverityNumber(), lr.SeverityText())
	}
	if attrStr(t, lr.Attributes(), "log.record.original") != line {
		t.Errorf("attributes = %v", lr.Attributes().AsRaw())
	}
	absent(t, lr.Attributes(), "original_message", "log.level")
	if want := time.Date(2025, 12, 31, 4, 3, 58, 0, time.UTC); !lr.Timestamp().AsTime().Equal(want) {
		t.Errorf("time = %v, want the time of the line", lr.Timestamp().AsTime())
	}
}

func TestLogrus_ZapLineWithAnError_AppendsItAndSplitsTheCaller(t *testing.T) {
	h := newHarness(t, defaultConfig())
	const line = `{"level":"error","ts":"2025-12-31T04:03:58Z","caller":"controller/reconciler.go:123","msg":"reconcile failed","error":"connection refused","controller":"backups","stacktrace":"goroutine 1"}`

	lr, _ := h.run(input{body: line})

	if lr.Body().Str() != "reconcile failed: connection refused" {
		t.Errorf("body = %q", lr.Body().Str())
	}
	a := lr.Attributes()
	if attrStr(t, a, "code.file.path") != "controller/reconciler.go" || attrStr(t, a, "exception.message") != "connection refused" ||
		attrStr(t, a, "exception.stacktrace") != "goroutine 1" || attrStr(t, a, "vstan.k8s.controller") != "backups" {
		t.Errorf("attributes = %v", a.AsRaw())
	}
	attrInt(t, a, "code.line.number", 123)
	absent(t, a, "code.filepath", "error.message", "error.stacktrace")
	if lr.SeverityNumber() != plog.SeverityNumberError {
		t.Errorf("severity = %d", lr.SeverityNumber())
	}
}

func TestLogrus_TextLine_KeepsTheExtraFieldsAndMapsWarning(t *testing.T) {
	h := newHarness(t, defaultConfig())

	lr, _ := h.run(input{body: `time="2025-12-31T04:25:29Z" level=warning msg="slow sync" duration=3s`})

	if lr.Body().Str() != "slow sync" || lr.SeverityNumber() != plog.SeverityNumberWarn || lr.SeverityText() != "WARN" {
		t.Errorf("body %q severity %d %q", lr.Body().Str(), lr.SeverityNumber(), lr.SeverityText())
	}
	if attrStr(t, lr.Attributes(), "vstan.logrus.duration") != "3s" {
		t.Errorf("attributes = %v", lr.Attributes().AsRaw())
	}
}

func TestLogrus_ZapConsoleLine_ReadsTheTabSeparatedFields(t *testing.T) {
	h := newHarness(t, defaultConfig())
	const line = "2026-01-02T14:18:43.362Z\twarn\tinternal/retry_sender.go:133\tExporting failed. Will retry.\t{\"attempt\":2}"

	lr, _ := h.run(input{body: line})

	if lr.Body().Str() != "Exporting failed. Will retry." || lr.SeverityNumber() != plog.SeverityNumberWarn {
		t.Errorf("body %q severity %d", lr.Body().Str(), lr.SeverityNumber())
	}
	a := lr.Attributes()
	if attrStr(t, a, "code.file.path") != "internal/retry_sender.go" {
		t.Errorf("attributes = %v", a.AsRaw())
	}
	attrInt(t, a, "code.line.number", 133)
	attrInt(t, a, "vstan.logrus.attempt", 2)
}

func TestLogrus_SeverityField_AndKnownLevelWords(t *testing.T) {
	h := newHarness(t, defaultConfig())
	for _, c := range []struct {
		line string
		num  plog.SeverityNumber
		text string
	}{
		{`{"severity":"WARNING","message":"x","timestamp":"2025-12-31T04:03:58Z"}`, plog.SeverityNumberWarn, "WARN"},
		{`{"level":"debug","msg":"x","time":"2025-12-31T04:03:58Z"}`, plog.SeverityNumberDebug, "DEBUG"},
		{`{"level":"panic","msg":"x","time":"2025-12-31T04:03:58Z"}`, plog.SeverityNumberFatal, "FATAL"},
		{`{"level":"dpanic","msg":"x","time":"2025-12-31T04:03:58Z"}`, plog.SeverityNumberFatal, "FATAL"},
	} {
		lr, _ := h.run(input{body: c.line})
		if lr.SeverityNumber() != c.num || lr.SeverityText() != c.text {
			t.Errorf("%s: %d %q, want %d %q", c.line, lr.SeverityNumber(), lr.SeverityText(), c.num, c.text)
		}
	}
}

func TestLogrus_UnknownLevel_StaysUnset(t *testing.T) {
	h := newHarness(t, defaultConfig())

	lr, _ := h.run(input{body: `{"level":"verbose","msg":"hello","time":"2025-12-31T04:03:58Z"}`})

	if lr.SeverityNumber() != plog.SeverityNumberUnspecified || lr.SeverityText() != "" {
		t.Errorf("severity = %d %q, want unset", lr.SeverityNumber(), lr.SeverityText())
	}
	if lr.Body().Str() != "hello" {
		t.Errorf("body = %q", lr.Body().Str())
	}
}

func TestLogrus_ParseTimestampOff_KeepsTheRecordTime(t *testing.T) {
	cfg := defaultConfig()
	cfg.Logrus.ParseTimestamp = false
	h := newHarness(t, cfg)

	lr, _ := h.run(input{body: `{"level":"info","msg":"x","time":"2025-12-31T04:03:58Z"}`, ts: recordTime})

	if !lr.Timestamp().AsTime().Equal(recordTime) {
		t.Errorf("time = %v", lr.Timestamp().AsTime())
	}
}

func TestLogrus_RewriteMessagesOff_KeepsTheBody(t *testing.T) {
	cfg := defaultConfig()
	cfg.Logrus.RewriteMessages = false
	h := newHarness(t, cfg)
	const line = `{"level":"info","msg":"x","time":"2025-12-31T04:03:58Z"}`

	lr, _ := h.run(input{body: line})

	if lr.Body().Str() != line {
		t.Errorf("body = %q", lr.Body().Str())
	}
}

func TestLogrus_JSONThatIsNotJSON_IsClaimedAndCountedAsAFailure(t *testing.T) {
	h := newHarness(t, defaultConfig())
	const line = `{"level":"info","msg":"broken", this is not json`

	lr, _ := h.run(input{body: line, attrs: map[string]any{"k": "v"}})

	if lr.Body().Str() != line || !maps.Equal(lr.Attributes().AsRaw(), map[string]any{"k": "v"}) {
		t.Errorf("record changed: %q %v", lr.Body().Str(), lr.Attributes().AsRaw())
	}
	if got := h.counter(parseFailedMetric, "format"); got["logrus"] != 1 {
		t.Errorf("parse_failed = %v", got)
	}
}

func TestLogrus_JSONWithoutALevelOrMessage_IsNotClaimed(t *testing.T) {
	h := newHarness(t, defaultConfig())

	lr, _ := h.run(input{body: `{"user":"x","count":3,"ok":true}`})

	if lr.Attributes().Len() != 0 {
		t.Errorf("attributes = %v", lr.Attributes().AsRaw())
	}
	if got := h.counter(unmatchedMetric, "source"); got["none"] != 1 {
		t.Errorf("unmatched = %v", got)
	}
}
