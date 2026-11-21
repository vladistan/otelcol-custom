// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

// cspell:ignore vstan

package logshapeprocessor

import (
	"testing"
	"time"

	"go.opentelemetry.io/collector/pdata/plog"
)

func TestPostgres_LogLine_GivesTheTextTheLevelThePidAndTheTime(t *testing.T) {
	h := newHarness(t, defaultConfig())
	const line = "2026-11-19 22:42:20.918 UTC [815116] LOG:  deleted 0 row(s)"

	lr, _ := h.run(input{body: line, ts: recordTime})

	if lr.Body().Str() != "deleted 0 row(s)" {
		t.Errorf("body = %q", lr.Body().Str())
	}
	if lr.SeverityNumber() != plog.SeverityNumberInfo || lr.SeverityText() != "INFO" {
		t.Errorf("severity = %d %q", lr.SeverityNumber(), lr.SeverityText())
	}
	attrInt(t, lr.Attributes(), "process.pid", 815116)
	if attrStr(t, lr.Attributes(), "log.record.original") != line {
		t.Errorf("attributes = %v", lr.Attributes().AsRaw())
	}
	if want := time.Date(2026, 11, 19, 22, 42, 20, 918000000, time.UTC); !lr.Timestamp().AsTime().Equal(want) {
		t.Errorf("time = %v, want %v", lr.Timestamp().AsTime(), want)
	}
}

func TestPostgres_ErrorWithContinuationLines_KeepsDetailAndStatement(t *testing.T) {
	h := newHarness(t, defaultConfig())
	const nl = "\n"
	body := `2025-12-31 00:01:39.618 UTC [1975] ERROR:  duplicate key value violates unique constraint "t_key"` + nl +
		"2025-12-31 00:01:39.618 UTC [1975] DETAIL:  Key (id)=(1) already exists." + nl +
		"2025-12-31 00:01:39.618 UTC [1975] STATEMENT:  INSERT INTO t VALUES (1)"

	lr, _ := h.run(input{body: body})

	if want := `duplicate key value violates unique constraint "t_key" (Key (id)=(1) already exists.)`; lr.Body().Str() != want {
		t.Errorf("body = %q", lr.Body().Str())
	}
	a := lr.Attributes()
	if attrStr(t, a, "vstan.postgres.detail") != "Key (id)=(1) already exists." || attrStr(t, a, "vstan.postgres.statement") != "INSERT INTO t VALUES (1)" {
		t.Errorf("attributes = %v", a.AsRaw())
	}
	if lr.SeverityNumber() != plog.SeverityNumberError {
		t.Errorf("severity = %d", lr.SeverityNumber())
	}
}

func TestPostgres_LevelWords(t *testing.T) {
	h := newHarness(t, defaultConfig())
	for _, c := range []struct {
		level string
		num   plog.SeverityNumber
		text  string
	}{
		{"WARNING", plog.SeverityNumberWarn, "WARN"},
		{"ERROR", plog.SeverityNumberError, "ERROR"},
		{"FATAL", plog.SeverityNumberFatal, "FATAL"},
		{"NOTICE", plog.SeverityNumberInfo, "INFO"},
		{"DEBUG3", plog.SeverityNumberDebug, "DEBUG"},
	} {
		lr, _ := h.run(input{body: "2026-11-19 22:42:20.918 UTC [1] " + c.level + ":  something happened"})
		if lr.SeverityNumber() != c.num || lr.SeverityText() != c.text {
			t.Errorf("%s: %d %q, want %d %q", c.level, lr.SeverityNumber(), lr.SeverityText(), c.num, c.text)
		}
	}
}

func TestPostgres_CentralEuropeanTime_IsConvertedToUTC(t *testing.T) {
	h := newHarness(t, defaultConfig())

	lr, _ := h.run(input{body: "2026-11-19 22:42:20.918 CET [12] LOG:  checkpoint complete", ts: recordTime})

	if want := time.Date(2026, 11, 19, 21, 42, 20, 918000000, time.UTC); !lr.Timestamp().AsTime().Equal(want) {
		t.Errorf("time = %v, want %v", lr.Timestamp().AsTime(), want)
	}
}

func TestPostgres_UnknownZone_KeepsTheRecordTimeAndStillShapesTheLine(t *testing.T) {
	h := newHarness(t, defaultConfig())

	lr, _ := h.run(input{body: "2026-11-19 22:42:20.918 XYZ [12] LOG:  hello", ts: recordTime})

	if !lr.Timestamp().AsTime().Equal(recordTime) {
		t.Errorf("time = %v, want the record time", lr.Timestamp().AsTime())
	}
	if lr.Body().Str() != "hello" {
		t.Errorf("body = %q", lr.Body().Str())
	}
}

func TestPostgres_TextInFullMessage_IsReadAndMoved(t *testing.T) {
	h := newHarness(t, defaultConfig())
	const line = "2026-11-19 22:42:20.918 UTC [7] LOG:  from the attribute"

	lr, _ := h.run(input{body: "", attrs: map[string]any{"full_message": line}})

	if lr.Body().Str() != "from the attribute" {
		t.Errorf("body = %q", lr.Body().Str())
	}
	absent(t, lr.Attributes(), "full_message")
	if attrStr(t, lr.Attributes(), "log.record.original") != line {
		t.Errorf("attributes = %v", lr.Attributes().AsRaw())
	}
}

func TestPostgres_ExtractDetailsOff_WritesNoDetailAttributes(t *testing.T) {
	cfg := defaultConfig()
	cfg.Postgres.ExtractDetails = false
	h := newHarness(t, cfg)
	const nl = "\n"
	body := "2025-12-31 00:01:39.618 UTC [1] ERROR:  bad" + nl + "2025-12-31 00:01:39.618 UTC [1] HINT:  try again"

	lr, _ := h.run(input{body: body})

	absent(t, lr.Attributes(), "vstan.postgres.hint", "vstan.postgres.detail")
}

func TestPostgres_RewriteOff_KeepsTheBody(t *testing.T) {
	cfg := defaultConfig()
	cfg.Postgres.RewriteMessages = false
	h := newHarness(t, cfg)
	const line = "2026-11-19 22:42:20.918 UTC [7] LOG:  keep me"

	lr, _ := h.run(input{body: line})

	if lr.Body().Str() != line {
		t.Errorf("body = %q", lr.Body().Str())
	}
}

func TestPostgres_ALineThatIsNotPostgres_IsNotClaimed(t *testing.T) {
	h := newHarness(t, defaultConfig())

	lr, _ := h.run(input{body: "2026-11-19 22:42:20 something else entirely, no pid and no level"})

	if lr.Attributes().Len() != 0 {
		t.Errorf("attributes = %v", lr.Attributes().AsRaw())
	}
	if got := h.counter(unmatchedMetric, "source"); got["none"] != 1 {
		t.Errorf("unmatched = %v", got)
	}
}

func TestPostgres_ADetailLineAlone_IsClaimedAndCountedAsAFailure(t *testing.T) {
	h := newHarness(t, defaultConfig())
	const line = "2025-12-31 00:01:39.618 UTC [1975] DETAIL:  Key (id)=(1) already exists."

	lr, _ := h.run(input{body: line})

	if lr.Body().Str() != line || lr.Attributes().Len() != 0 {
		t.Errorf("record changed: %q %v", lr.Body().Str(), lr.Attributes().AsRaw())
	}
	if got := h.counter(parseFailedMetric, "format"); got["postgres"] != 1 {
		t.Errorf("parse_failed = %v", got)
	}
}
