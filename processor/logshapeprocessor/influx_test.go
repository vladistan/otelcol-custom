// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

// cspell:ignore vstan

import (
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/collector/pdata/plog"
)

const influxLine = `[httpd] 192.0.2.19 - webuser [20/Nov/2026:00:02:45 +0100] "POST /write?db=zero HTTP/1.1" 204 0 "-" "InfluxDBClient" 5a1b2c3d-1111-2222-3333-444455556666 14684`

func influxWith(status string) string {
	return strings.Replace(influxLine, `" 204 0`, `" `+status+` 0`, 1)
}

func TestConsume_Influx_RealLine_ShapesAllAttributes(t *testing.T) {
	h := newHarness(t, defaultConfig())

	lr, _ := h.run(input{body: influxLine})

	if got := lr.Body().Str(); got != "POST /write db=zero 204 14.7ms" {
		t.Fatalf("body = %q", got)
	}
	a := lr.Attributes()
	for k, want := range map[string]string{
		"http.request.method":     "POST",
		"url.path":                "/write",
		"url.query":               "db=zero",
		"client.address":          "192.0.2.19",
		"user_agent.original":     "InfluxDBClient",
		"user.name":               "webuser",
		"vstan.influx.request_id": "5a1b2c3d-1111-2222-3333-444455556666",
		"log.record.original":     influxLine,
	} {
		if got := attrStr(t, a, k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	attrInt(t, a, "http.response.status_code", 204)
	want := time.Date(2026, 11, 19, 23, 2, 45, 0, time.UTC)
	if !lr.Timestamp().AsTime().Equal(want) {
		t.Errorf("time = %v, want %v", lr.Timestamp().AsTime(), want)
	}
}

func TestConsume_Influx_Severity_FollowsTheStatusClass(t *testing.T) {
	for status, want := range map[string]plog.SeverityNumber{
		"204": plog.SeverityNumberInfo,
		"302": plog.SeverityNumberInfo,
		"404": plog.SeverityNumberWarn,
		"503": plog.SeverityNumberError,
	} {
		h := newHarness(t, defaultConfig())
		lr, _ := h.run(input{body: influxWith(status)})
		if lr.SeverityNumber() != want {
			t.Errorf("status %s: severity = %d, want %d", status, lr.SeverityNumber(), want)
		}
	}
}

func TestConsume_Influx_NoUserNoQuery_OmitsThoseAttributes(t *testing.T) {
	h := newHarness(t, defaultConfig())
	line := `[httpd] 192.0.2.19 - - [20/Nov/2026:00:02:45 +0100] "GET /ping HTTP/1.1" 204 0 "-" "curl/8" 9f9f9f9f-0000-1111-2222-333344445555 80`

	lr, _ := h.run(input{body: line})

	if got := lr.Body().Str(); got != "GET /ping 204 80us" {
		t.Fatalf("body = %q", got)
	}
	absent(t, lr.Attributes(), "url.query", "user.name")
}

func TestConsume_Influx_OtherLines_PassThroughUnmatched(t *testing.T) {
	for _, line := range []string{
		`192.0.2.19 - webuser [20/Nov/2026:00:02:45 +0100] "POST /write HTTP/1.1" 204 0`,
		`plain text`,
	} {
		h := newHarness(t, defaultConfig())
		lr, _ := h.run(input{body: line})
		if lr.Body().Str() != line {
			t.Errorf("body changed: %q", lr.Body().Str())
		}
		absent(t, lr.Attributes(), "log.record.original", "http.request.method")
		if got := h.counter(unmatchedMetric, "source"); got["none"] != 1 {
			t.Errorf("%q: unmatched counter = %v", line, got)
		}
	}
}

func TestConsume_Influx_LogfmtLine_IsOwnedByTheLogfmtBranch(t *testing.T) {
	const line = `ts=2026-11-20T00:02:45.1Z lvl=info msg="Executing query" log_id=0abc`

	lr, _ := newHarness(t, defaultConfig()).run(input{body: line})
	if got := lr.Body().Str(); got != "Executing query log_id=0abc" {
		t.Errorf("default config body = %q", got)
	}
	if lr.SeverityNumber() != plog.SeverityNumberInfo {
		t.Errorf("severity = %d", lr.SeverityNumber())
	}
	if got := attrStr(t, lr.Attributes(), "log.record.original"); got != line {
		t.Errorf("log.record.original = %q", got)
	}

	cfg := defaultConfig()
	cfg.Branches = []string{"influx"}
	h := newHarness(t, cfg)
	lr, _ = h.run(input{body: line})
	if lr.Body().Str() != line {
		t.Errorf("influx-only body = %q", lr.Body().Str())
	}
	absent(t, lr.Attributes(), "log.record.original")
	if got := h.counter(unmatchedMetric, "source"); got["none"] != 1 {
		t.Errorf("influx-only unmatched = %v", got)
	}
}

func TestConsume_Influx_Malformed_CountsParseFailedAndKeepsRecord(t *testing.T) {
	huge := `[httpd] ` + strings.Repeat("x", 1<<20)
	for name, line := range map[string]string{
		"prefix":    "[httpd] ",
		"truncated": `[httpd] 192.0.2.19 - webuser [20/Nov/2026:00:02:45 +0100] "POST /wri`,
		"badtime":   `[httpd] 192.0.2.19 - u [99/Nov/2026:00:02:45 +0100] "GET / HTTP/1.1" 200 0 "-" "ua" id 5`,
		"binary":    "[httpd] \x00\xff\xfe\x01 garbage \x80",
		"huge":      huge,
	} {
		h := newHarness(t, defaultConfig())
		lr, _ := h.run(input{body: line})
		if lr.Body().Str() != line {
			t.Errorf("%s: body changed", name)
		}
		absent(t, lr.Attributes(), "log.record.original", "http.request.method")
		if got := h.counter(parseFailedMetric, "format"); got["influx"] != 1 || len(got) != 1 {
			t.Errorf("%s: parse_failed = %v", name, got)
		}
		if got := h.counter(unmatchedMetric, "source"); len(got) != 0 {
			t.Errorf("%s: unmatched = %v", name, got)
		}
	}
}

func TestConsume_Influx_EmptyBody_CountsUnmatched(t *testing.T) {
	h := newHarness(t, defaultConfig())

	lr, _ := h.run(input{body: ""})

	absent(t, lr.Attributes(), "log.record.original")
	if got := h.counter(unmatchedMetric, "source"); got["none"] != 1 {
		t.Errorf("unmatched = %v", got)
	}
	if got := h.counter(parseFailedMetric, "format"); len(got) != 0 {
		t.Errorf("parse_failed = %v", got)
	}
}

func TestConsume_Influx_ExistingOriginal_IsKept(t *testing.T) {
	h := newHarness(t, defaultConfig())

	lr, _ := h.run(input{body: influxLine, attrs: map[string]any{"log.record.original": "earlier raw line"}})

	if got := attrStr(t, lr.Attributes(), "log.record.original"); got != "earlier raw line" {
		t.Errorf("log.record.original = %q", got)
	}
	if got := lr.Body().Str(); got != "POST /write db=zero 204 14.7ms" {
		t.Errorf("body = %q", got)
	}
}
