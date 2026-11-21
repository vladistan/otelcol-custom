// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

// cspell:ignore vstan

package logshapeprocessor

import (
	"testing"
	"time"

	"go.opentelemetry.io/collector/pdata/plog"
)

func TestRedis_NoticeLine_GivesTheTextThePidTheRoleAndTheTime(t *testing.T) {
	h := newHarness(t, defaultConfig())
	const line = "1:M 19 Nov 2026 23:22:37.151 * Background saving terminated with success"

	lr, res := h.run(input{body: line, ts: recordTime})

	if lr.Body().Str() != "Background saving terminated with success" {
		t.Errorf("body = %q", lr.Body().Str())
	}
	if lr.SeverityNumber() != plog.SeverityNumberInfo || lr.SeverityText() != "INFO" {
		t.Errorf("severity = %d %q", lr.SeverityNumber(), lr.SeverityText())
	}
	a := lr.Attributes()
	attrInt(t, a, "process.pid", 1)
	if attrStr(t, a, "vstan.redis.role") != "master" || attrStr(t, a, "log.record.original") != line {
		t.Errorf("attributes = %v", a.AsRaw())
	}
	absent(t, a, "redis.pid", "redis.level", "original_message")
	if want := time.Date(2026, 11, 19, 23, 22, 37, 151000000, time.UTC); !lr.Timestamp().AsTime().Equal(want) {
		t.Errorf("time = %v, want %v", lr.Timestamp().AsTime(), want)
	}
	if attrStr(t, res.Attributes(), "service.name") != "redis" {
		t.Errorf("resource = %v", res.Attributes().AsRaw())
	}
}

func TestRedis_LevelSymbols(t *testing.T) {
	h := newHarness(t, defaultConfig())
	for _, c := range []struct {
		symbol string
		num    plog.SeverityNumber
		text   string
	}{
		{".", plog.SeverityNumberDebug, "DEBUG"},
		{"-", plog.SeverityNumberInfo, "INFO"},
		{"*", plog.SeverityNumberInfo, "INFO"},
		{"#", plog.SeverityNumberWarn, "WARN"},
	} {
		lr, _ := h.run(input{body: "5688:C 31 Dec 2025 03:04:22.141 " + c.symbol + " RDB: 0 MB of memory used by copy-on-write"})
		if lr.SeverityNumber() != c.num || lr.SeverityText() != c.text {
			t.Errorf("%q: %d %q, want %d %q", c.symbol, lr.SeverityNumber(), lr.SeverityText(), c.num, c.text)
		}
	}
}

func TestRedis_ServiceNameAlreadySet_IsKept(t *testing.T) {
	h := newHarness(t, defaultConfig())

	_, res := h.run(input{body: "1:M 19 Nov 2026 23:22:37.151 * ready", resource: map[string]any{"service.name": "cache"}})

	if attrStr(t, res.Attributes(), "service.name") != "cache" {
		t.Errorf("resource = %v", res.Attributes().AsRaw())
	}
}

func TestRedis_SetServiceNameOff_LeavesTheResource(t *testing.T) {
	cfg := defaultConfig()
	cfg.Redis.SetServiceName = false
	h := newHarness(t, cfg)

	_, res := h.run(input{body: "1:M 19 Nov 2026 23:22:37.151 * ready"})

	absent(t, res.Attributes(), "service.name")
}

func TestRedis_TextInFullMessage_IsMovedToTheOriginal(t *testing.T) {
	h := newHarness(t, defaultConfig())
	const line = "1:S 19 Nov 2026 23:22:37.151 # replica lost"

	lr, _ := h.run(input{body: line, attrs: map[string]any{"full_message": "edge copy of the line"}})

	absent(t, lr.Attributes(), "full_message")
	if attrStr(t, lr.Attributes(), "log.record.original") != "edge copy of the line" {
		t.Errorf("attributes = %v", lr.Attributes().AsRaw())
	}
	if lr.Body().Str() != "replica lost" {
		t.Errorf("body = %q", lr.Body().Str())
	}
}

func TestRedis_RewriteOff_KeepsTheBody(t *testing.T) {
	cfg := defaultConfig()
	cfg.Redis.RewriteMessages = false
	h := newHarness(t, cfg)
	const line = "1:M 19 Nov 2026 23:22:37.151 * keep me"

	lr, _ := h.run(input{body: line})

	if lr.Body().Str() != line {
		t.Errorf("body = %q", lr.Body().Str())
	}
}

func TestRedis_BodyThatLooksLikeRedisButIsNot_IsClaimedAndCountedAsAFailure(t *testing.T) {
	h := newHarness(t, defaultConfig())
	const line = "12:M 99 xx 2026 not a redis line but it starts like one"

	lr, _ := h.run(input{body: line})

	if lr.Body().Str() != line || lr.Attributes().Len() != 0 {
		t.Errorf("record changed: %q %v", lr.Body().Str(), lr.Attributes().AsRaw())
	}
	if got := h.counter(parseFailedMetric, "format"); got["redis"] != 1 {
		t.Errorf("parse_failed = %v", got)
	}
}

func TestRedis_JournaldTextBody_IsRead(t *testing.T) {
	h := newHarness(t, defaultConfig())
	ld := plog.NewLogs()
	lr := ld.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty().LogRecords().AppendEmpty()
	lr.Body().SetEmptyMap().PutStr("text", "1:M 19 Nov 2026 23:22:37.151 * from a map body")

	out := h.runBatch(ld)

	got := out.ResourceLogs().At(0).ScopeLogs().At(0).LogRecords().At(0)
	if got.Body().Str() != "from a map body" {
		t.Errorf("body = %v", got.Body().AsRaw())
	}
}
