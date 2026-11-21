// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

// cspell:ignore dpanic eror NOERROR NXDOMAIN

package severity

import "testing"

func TestFromSource_EachLevelSource_GivesNumberAndMatchingText(t *testing.T) {
	cases := []struct {
		name   string
		source Source
		raw    string
		number int32
		text   string
	}{
		{"uvicorn info", Word, "INFO", 9, "INFO"},
		{"bracket tag lowercase", Word, "info", 9, "INFO"},
		{"logfmt warning", Word, "warning", 13, "WARN"},
		{"logfmt lvl eror", Word, "eror", 17, "ERROR"},
		{"ssm-agent debug", Word, "DEBUG", 5, "DEBUG"},
		{"slog fatal", Word, "FATAL", 21, "FATAL"},
		{"zap development panic", Word, "dpanic", 21, "FATAL"},
		{"logrus panic", Word, "panic", 21, "FATAL"},
		{"tracing trace", Word, "TRACE", 1, "TRACE"},
		{"gcp structured notice", Word, "notice", 9, "INFO"},
		{"gcp structured critical", Word, "critical", 21, "FATAL"},
		{"klog I", Klog, "I", 9, "INFO"},
		{"klog W", Klog, "W", 13, "WARN"},
		{"klog E", Klog, "E", 17, "ERROR"},
		{"klog F", Klog, "F", 21, "FATAL"},
		{"mongod I", Mongod, "I", 9, "INFO"},
		{"mongod W", Mongod, "W", 13, "WARN"},
		{"mongod E", Mongod, "E", 17, "ERROR"},
		{"mongod F", Mongod, "F", 21, "FATAL"},
		{"mongod D1", Mongod, "D1", 5, "DEBUG"},
		{"mongod D5", Mongod, "D5", 5, "DEBUG"},
		{"clickhouse Information", ClickHouse, "Information", 9, "INFO"},
		{"clickhouse Warning", ClickHouse, "Warning", 13, "WARN"},
		{"clickhouse Error", ClickHouse, "Error", 17, "ERROR"},
		{"clickhouse Trace", ClickHouse, "Trace", 1, "TRACE"},
		{"clickhouse Debug", ClickHouse, "Debug", 5, "DEBUG"},
		{"clickhouse Fatal", ClickHouse, "Fatal", 21, "FATAL"},
		{"postgres LOG", Postgres, "LOG", 9, "INFO"},
		{"postgres WARNING", Postgres, "WARNING", 13, "WARN"},
		{"postgres ERROR", Postgres, "ERROR", 17, "ERROR"},
		{"postgres FATAL", Postgres, "FATAL", 21, "FATAL"},
		{"postgres DEBUG2", Postgres, "DEBUG2", 5, "DEBUG"},
		{"pino 10", Pino, "10", 1, "TRACE"},
		{"pino 20", Pino, "20", 5, "DEBUG"},
		{"pino 30", Pino, "30", 9, "INFO"},
		{"pino 40", Pino, "40", 13, "WARN"},
		{"pino 50", Pino, "50", 17, "ERROR"},
		{"pino 60", Pino, "60", 21, "FATAL"},
		{"redis dot", Redis, ".", 5, "DEBUG"},
		{"redis dash", Redis, "-", 9, "INFO"},
		{"redis star", Redis, "*", 9, "INFO"},
		{"redis hash", Redis, "#", 13, "WARN"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := FromSource(c.source, c.raw)
			if !ok {
				t.Fatalf("FromSource(%v, %q) not found", c.source, c.raw)
			}
			if got.Number != c.number || got.Text != c.text {
				t.Fatalf("FromSource(%v, %q) = %d %q, want %d %q", c.source, c.raw, got.Number, got.Text, c.number, c.text)
			}
		})
	}
}

func TestFromSource_UnknownValue_StaysUnset(t *testing.T) {
	for _, c := range []struct {
		source Source
		raw    string
	}{
		{Word, ""},
		{Word, "banana"},
		{Klog, "X"},
		{Mongod, "D6"},
		{Pino, "35"},
		{Pino, "abc"},
		{Redis, "?"},
		{Postgres, "NOTICE2"},
	} {
		got, ok := FromSource(c.source, c.raw)
		if ok || got != (Level{}) {
			t.Errorf("FromSource(%v, %q) = %+v, %v; want unset", c.source, c.raw, got, ok)
		}
	}
}

func TestFromHTTPStatus_UsesTheStatusRule(t *testing.T) {
	for _, c := range []struct {
		status int
		number int32
		text   string
		ok     bool
	}{
		{200, 9, "INFO", true},
		{204, 9, "INFO", true},
		{302, 9, "INFO", true},
		{399, 9, "INFO", true},
		{400, 13, "WARN", true},
		{404, 13, "WARN", true},
		{499, 13, "WARN", true},
		{500, 17, "ERROR", true},
		{502, 17, "ERROR", true},
		{599, 17, "ERROR", true},
		{101, 9, "INFO", true},
		{0, 0, "", false},
		{99, 0, "", false},
		{600, 0, "", false},
	} {
		got, ok := FromHTTPStatus(c.status)
		if ok != c.ok || got.Number != c.number || got.Text != c.text {
			t.Errorf("FromHTTPStatus(%d) = %+v, %v; want %d %q %v", c.status, got, ok, c.number, c.text, c.ok)
		}
	}
}

func TestFromDNSResponse_ServfailAndRefusedWarn(t *testing.T) {
	for _, c := range []struct {
		status string
		number int32
		ok     bool
	}{
		{"NOERROR", 9, true},
		{"NXDOMAIN", 9, true},
		{"SERVFAIL", 13, true},
		{"REFUSED", 13, true},
		{"servfail", 13, true},
		{"FORMERR", 0, false},
	} {
		got, ok := FromDNSResponse(c.status)
		if ok != c.ok || got.Number != c.number {
			t.Errorf("FromDNSResponse(%q) = %+v, %v; want %d %v", c.status, got, ok, c.number, c.ok)
		}
	}
}

func TestPick_EmbeddedLevelBeatsStreamHint(t *testing.T) {
	embedded := Level{Number: 9, Text: "INFO"}
	hint := Level{Number: 17, Text: "ERROR"}
	got := Pick(embedded, true, hint, true)
	if got != embedded {
		t.Fatalf("litellm line with INFO inside and ERROR priority = %+v, want the embedded INFO", got)
	}
}

func TestPick_HintAloneIsIgnored(t *testing.T) {
	hint := Level{Number: 17, Text: "ERROR"}
	if got := Pick(Level{}, false, hint, true); got != (Level{}) {
		t.Fatalf("a stderr-only hint gave %+v, want unset", got)
	}
}

func TestPick_NothingKnown_StaysUnset(t *testing.T) {
	if got := Pick(Level{}, false, Level{}, false); got != (Level{}) {
		t.Fatalf("got %+v, want unset", got)
	}
}

func TestFromNumber_TextAlwaysMatchesTheNumber(t *testing.T) {
	for n := int32(1); n <= 24; n++ {
		got := FromNumber(n)
		if got.Number != n {
			t.Errorf("FromNumber(%d).Number = %d", n, got.Number)
		}
		if got.Text == "" {
			t.Errorf("FromNumber(%d) has no text", n)
		}
		if again := FromNumber(got.Number); again != got {
			t.Errorf("FromNumber(%d) not stable: %+v then %+v", n, got, again)
		}
	}
	if got := FromNumber(0); got != (Level{}) {
		t.Errorf("FromNumber(0) = %+v, want unset", got)
	}
	if got := FromNumber(25); got != (Level{}) {
		t.Errorf("FromNumber(25) = %+v, want unset", got)
	}
}

func TestMergeMap_ScalarMapAndListFollowTheOverrideRules(t *testing.T) {
	defaults := map[string]Level{"WARNING": {Number: 13, Text: "WARN"}, "LOG": {Number: 9, Text: "INFO"}}
	got := MergeMap(defaults, map[string]Level{"LOG": {Number: 5, Text: "DEBUG"}, "PANIC": {Number: 21, Text: "FATAL"}})
	if got["LOG"].Number != 5 || got["PANIC"].Number != 21 || got["WARNING"].Number != 13 {
		t.Fatalf("per-key replace failed: %+v", got)
	}
	if defaults["LOG"].Number != 9 {
		t.Fatalf("defaults were modified: %+v", defaults)
	}
}
