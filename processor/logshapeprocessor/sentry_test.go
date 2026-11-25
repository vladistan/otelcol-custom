// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"testing"
	"time"

	"go.opentelemetry.io/collector/pdata/plog"
)

const sentryAccess = "02:30:21 [INFO] sentry.access.api: api.access " +
	"(method='GET' view='sentry.web.frontend.home.HomeView' response='302' path='/api/0/projects/' " +
	"caller_ip='10.0.0.7' user_agent='ELB-HealthChecker/2.0' request_duration_seconds='0.012')"

var sentryRecordTime = time.Date(2026, 11, 20, 2, 30, 25, 0, time.UTC)

func TestSentry_AccessLine_GivesTheHTTPBodyTheAttributesAndTheScope(t *testing.T) {
	h := newHarness(t, defaultConfig())
	ld := input{body: sentryAccess, ts: sentryRecordTime}.logs()

	out := h.runBatch(ld)

	rl := out.ResourceLogs().At(0)
	lr := rl.ScopeLogs().At(0).LogRecords().At(0)
	if got := rl.ScopeLogs().At(0).Scope().Name(); got != "sentry.access.api" {
		t.Errorf("scope name = %q", got)
	}
	if lr.Body().Str() != "GET /api/0/projects/ 302 12ms" {
		t.Errorf("body = %q", lr.Body().Str())
	}
	a := lr.Attributes()
	for k, want := range map[string]string{
		"http.request.method": "GET", "url.path": "/api/0/projects/", "client.address": "10.0.0.7",
		"user_agent.original": "ELB-HealthChecker/2.0", "vstan.http.request.duration": "0.012",
		"vstan.sentry.view": "sentry.web.frontend.home.HomeView", "vstan.sentry.message_type": "api.access",
		"log.record.original": sentryAccess,
	} {
		if got := attrStr(t, a, k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	attrInt(t, a, "http.response.status_code", 302)
	absent(t, a, "sentry.logger", "sentry.level", "original_message", "http.request.duration")
	if lr.SeverityNumber() != plog.SeverityNumberInfo || lr.SeverityText() != "INFO" {
		t.Errorf("severity = %d %q", lr.SeverityNumber(), lr.SeverityText())
	}
	if want := time.Date(2026, 11, 20, 2, 30, 21, 0, time.UTC); !lr.Timestamp().AsTime().Equal(want) {
		t.Errorf("time = %v, want %v", lr.Timestamp().AsTime(), want)
	}
	if v, _ := rl.Resource().Attributes().Get("service.name"); v.Str() != "sentry" {
		t.Errorf("resource = %v", rl.Resource().Attributes().AsRaw())
	}
}

func TestSentry_StatusRule_BeatsTheInfoLevel(t *testing.T) {
	h := newHarness(t, defaultConfig())
	for _, c := range []struct {
		status string
		num    plog.SeverityNumber
	}{{"404", plog.SeverityNumberWarn}, {"500", plog.SeverityNumberError}, {"200", plog.SeverityNumberInfo}} {
		line := "02:30:21 [INFO] sentry.access.api: api.access (method='GET' response='" + c.status + "' path='/x')"
		lr, _ := h.run(input{body: line, ts: sentryRecordTime})
		if lr.SeverityNumber() != c.num {
			t.Errorf("%s: severity %d, want %d", c.status, lr.SeverityNumber(), c.num)
		}
	}
}

func TestSentry_SimpleLine_GivesTheMessageTheLevelAndTheScope(t *testing.T) {
	h := newHarness(t, defaultConfig())

	out := h.runBatch(input{body: "02:30:21 [WARNING] sentry.tasks: Retrying the task", ts: sentryRecordTime}.logs())

	sl := out.ResourceLogs().At(0).ScopeLogs().At(0)
	lr := sl.LogRecords().At(0)
	if lr.Body().Str() != "Retrying the task" || sl.Scope().Name() != "sentry.tasks" {
		t.Errorf("body %q scope %q", lr.Body().Str(), sl.Scope().Name())
	}
	if lr.SeverityNumber() != plog.SeverityNumberWarn || lr.SeverityText() != "WARN" {
		t.Errorf("severity = %d %q", lr.SeverityNumber(), lr.SeverityText())
	}
}

func TestSentry_UnknownLevel_StaysUnset(t *testing.T) {
	h := newHarness(t, defaultConfig())

	lr, _ := h.run(input{body: "02:30:21 [VERBOSE] sentry.tasks: Something", ts: sentryRecordTime})

	if lr.SeverityNumber() != plog.SeverityNumberUnspecified || lr.SeverityText() != "" {
		t.Errorf("severity = %d %q, want unset", lr.SeverityNumber(), lr.SeverityText())
	}
}

func TestSentry_LineTimeJustBeforeMidnight_BelongsToTheDayBefore(t *testing.T) {
	h := newHarness(t, defaultConfig())
	rec := time.Date(2026, 11, 20, 0, 0, 30, 0, time.UTC)

	lr, _ := h.run(input{body: "23:59:58 [INFO] sentry.tasks: late line", ts: rec})

	if want := time.Date(2026, 11, 19, 23, 59, 58, 0, time.UTC); !lr.Timestamp().AsTime().Equal(want) {
		t.Errorf("time = %v, want %v", lr.Timestamp().AsTime(), want)
	}
}

func TestSentry_NoRecordTime_LeavesTheTimeUnset(t *testing.T) {
	h := newHarness(t, defaultConfig())

	lr, _ := h.run(input{body: "02:30:21 [INFO] sentry.tasks: no date to borrow"})

	if lr.Timestamp() != 0 {
		t.Errorf("time = %v, want unset", lr.Timestamp().AsTime())
	}
}

func TestSentry_ParseTimestampOff_KeepsTheRecordTime(t *testing.T) {
	cfg := defaultConfig()
	cfg.Sentry.ParseTimestamp = false
	h := newHarness(t, cfg)

	lr, _ := h.run(input{body: sentryAccess, ts: sentryRecordTime})

	if !lr.Timestamp().AsTime().Equal(sentryRecordTime) {
		t.Errorf("time = %v", lr.Timestamp().AsTime())
	}
}

func TestSentry_ExtractHTTPFieldsOff_WritesNoHTTPAttributes(t *testing.T) {
	cfg := defaultConfig()
	cfg.Sentry.ExtractHTTPFields = false
	h := newHarness(t, cfg)

	lr, _ := h.run(input{body: sentryAccess, ts: sentryRecordTime})

	absent(t, lr.Attributes(), "http.request.method", "url.path", "http.response.status_code", "user_agent.original", "client.address")
}

func TestSentry_TextInFullMessage_IsMovedToTheOriginal(t *testing.T) {
	h := newHarness(t, defaultConfig())

	lr, _ := h.run(input{body: "02:30:21 [INFO] sentry.tasks: hello", attrs: map[string]any{"full_message": "edge copy"}, ts: sentryRecordTime})

	absent(t, lr.Attributes(), "full_message")
	if attrStr(t, lr.Attributes(), "log.record.original") != "edge copy" {
		t.Errorf("attributes = %v", lr.Attributes().AsRaw())
	}
}

func TestSentry_ALineWithoutALevelBracket_IsNotClaimed(t *testing.T) {
	h := newHarness(t, defaultConfig())

	lr, _ := h.run(input{body: "02:30:21 hello world with no bracket at all in it"})

	if lr.Attributes().Len() != 0 {
		t.Errorf("attributes = %v", lr.Attributes().AsRaw())
	}
	if got := h.counter(unmatchedMetric, "source"); got["none"] != 1 {
		t.Errorf("unmatched = %v", got)
	}
}

func TestSentry_ShortLineThatLooksLikeAClock_IsNotClaimedAndDoesNotCrash(t *testing.T) {
	h := newHarness(t, defaultConfig())

	lr, _ := h.run(input{body: "23:01:22 - done: all good"})

	if lr.Body().Str() != "23:01:22 - done: all good" || lr.Attributes().Len() != 0 {
		t.Errorf("record changed: %q %v", lr.Body().Str(), lr.Attributes().AsRaw())
	}
	if got := h.counter(unmatchedMetric, "source"); got["none"] != 1 {
		t.Errorf("unmatched = %v", got)
	}
}
