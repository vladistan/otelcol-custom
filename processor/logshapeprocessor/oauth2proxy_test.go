// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

// cspell:ignore vstan

import (
	"strings"
	"testing"

	"go.opentelemetry.io/collector/pdata/plog"
)

const oauth2ProxyProbeLine = `198.51.100.23 - 7f1e2d3c-aaaa-bbbb-cccc-ddddeeeeffff - - [2026/11/20 04:42:11] 203.0.113.46:4180 GET - "/ready" HTTP/1.1 "kube-probe/1.36" 200 2 0.000`

func oauth2ProxyLine(status, user, path string) string {
	return `198.51.100.23 - 7f1e2d3c-aaaa-bbbb-cccc-ddddeeeeffff - ` + user + ` [2026/11/20 04:42:11] 203.0.113.46:4180 GET - "` + path + `" HTTP/1.1 "Mozilla/5.0 (X11)" ` + status + ` 512 0.123`
}

func TestConsume_OAuth2Proxy_ProbeLine_ShapesAllAttributes(t *testing.T) {
	h := newHarness(t, defaultConfig())

	lr, _ := h.run(input{body: oauth2ProxyProbeLine})

	if got := lr.Body().Str(); got != "GET /ready 200 2B 0ms" {
		t.Fatalf("body = %q", got)
	}
	a := lr.Attributes()
	for k, want := range map[string]string{
		"http.request.method":          "GET",
		"url.path":                     "/ready",
		"client.address":               "198.51.100.23",
		"user_agent.original":          "kube-probe/1.36",
		"vstan.oauth2proxy.request_id": "7f1e2d3c-aaaa-bbbb-cccc-ddddeeeeffff",
		"log.record.original":          oauth2ProxyProbeLine,
	} {
		if got := attrStr(t, a, k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	attrInt(t, a, "http.response.status_code", 200)
	absent(t, a, "user.name", "url.query")
}

func TestConsume_OAuth2Proxy_UserField_BecomesUserName(t *testing.T) {
	h := newHarness(t, defaultConfig())

	lr, _ := h.run(input{body: oauth2ProxyLine("302", "someone@example.org", "/oauth2/auth?rd=%2F")})

	a := lr.Attributes()
	if got := attrStr(t, a, "user.name"); got != "someone@example.org" {
		t.Fatalf("user.name = %q", got)
	}
	if got := attrStr(t, a, "url.query"); got != "rd=%2F" {
		t.Fatalf("url.query = %q", got)
	}
	if got := lr.Body().Str(); got != "GET /oauth2/auth rd=%2F 302 512B 123ms" {
		t.Fatalf("body = %q", got)
	}
}

func TestConsume_OAuth2Proxy_Severity_FollowsTheStatusClass(t *testing.T) {
	for status, want := range map[string]plog.SeverityNumber{
		"202": plog.SeverityNumberInfo,
		"302": plog.SeverityNumberInfo,
		"401": plog.SeverityNumberWarn,
		"500": plog.SeverityNumberError,
	} {
		h := newHarness(t, defaultConfig())
		lr, _ := h.run(input{body: oauth2ProxyLine(status, "-", "/x")})
		if lr.SeverityNumber() != want {
			t.Errorf("status %s: severity = %d, want %d", status, lr.SeverityNumber(), want)
		}
	}
}

func TestConsume_OAuth2Proxy_OtherLines_PassThroughUnmatched(t *testing.T) {
	for _, line := range []string{
		`[2026/11/20 04:42:11] [AuthSuccess] Authenticated via OAuth2: Session{email:someone@example.org user:abc token:true}`,
		`[2026/11/20 04:42:11] [main.go:91] WARNING: You are using alpha configuration`,
		`[oauthproxy.go:1101] No valid authentication in request. Initiating login.`,
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

func TestConsume_OAuth2Proxy_Malformed_PassesThroughUnmatched(t *testing.T) {
	// The claim runs the same parse as the shaping, so no line is claimed and then fails.
	for name, line := range map[string]string{
		"empty":     "",
		"truncated": `198.51.100.23 - 7f1e2d3c-aaaa - - [2026/11/20 04:42:11] 203.0.113.46:4180 GET - "/rea`,
		"badtime":   `198.51.100.23 - id - - [2026/13/45 99:99:99] 203.0.113.46:4180 GET - "/x" HTTP/1.1 "ua" 200 2 0.000`,
		"binary":    "\x00\xff\xfe - \x01 - - [\x80]",
		"huge":      strings.Repeat("198.51.100.23 - id - ", 1<<16),
	} {
		h := newHarness(t, defaultConfig())
		lr, _ := h.run(input{body: line})
		if lr.Body().Str() != line {
			t.Errorf("%s: body changed", name)
		}
		absent(t, lr.Attributes(), "log.record.original", "http.request.method")
		if got := h.counter(unmatchedMetric, "source"); got["none"] != 1 {
			t.Errorf("%s: unmatched = %v", name, got)
		}
		if got := h.counter(parseFailedMetric, "format"); len(got) != 0 {
			t.Errorf("%s: parse_failed = %v", name, got)
		}
	}
}

func TestConsume_OAuth2Proxy_ExistingOriginal_IsKept(t *testing.T) {
	h := newHarness(t, defaultConfig())

	lr, _ := h.run(input{body: oauth2ProxyProbeLine, attrs: map[string]any{"log.record.original": "earlier raw line"}})

	if got := attrStr(t, lr.Attributes(), "log.record.original"); got != "earlier raw line" {
		t.Errorf("log.record.original = %q", got)
	}
	if got := lr.Body().Str(); got != "GET /ready 200 2B 0ms" {
		t.Errorf("body = %q", got)
	}
}
