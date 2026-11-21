// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

// cspell:ignore SERVFAIL NXDOMAIN NOERROR NOTIMP rcode vstan

import (
	"strings"
	"testing"

	"go.opentelemetry.io/collector/pdata/plog"
)

const coreDNSLine = `[INFO] 198.51.100.42:56818 - 13677 "A IN nohost. udp 24 false 512" SERVFAIL qr,aa,rd,ra 24 0.000070318s`

func coreDNSWith(rcode string) string {
	return strings.Replace(coreDNSLine, "SERVFAIL", rcode, 1)
}

func TestConsume_CoreDNS_QueryLine_ShapesAllAttributes(t *testing.T) {
	h := newHarness(t, defaultConfig())

	lr, _ := h.run(input{body: coreDNSLine})

	if got := lr.Body().Str(); got != "SERVFAIL A nohost. 70us" {
		t.Fatalf("body = %q", got)
	}
	a := lr.Attributes()
	for k, want := range map[string]string{
		"client.address":          "198.51.100.42",
		"dns.question.name":       "nohost.",
		"vstan.dns.question.type": "A",
		"vstan.dns.response_code": "SERVFAIL",
		"log.record.original":     coreDNSLine,
	} {
		if got := attrStr(t, a, k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	attrInt(t, a, "client.port", 56818)
	v, ok := a.Get("vstan.dns.duration")
	if !ok || v.Double() < 0.00007 || v.Double() > 0.00008 {
		t.Errorf("vstan.dns.duration = %v", a.AsRaw())
	}
}

func TestConsume_CoreDNS_Severity_FollowsTheResponseCode(t *testing.T) {
	cases := map[string]plog.SeverityNumber{
		"NOERROR":  plog.SeverityNumberInfo,
		"NXDOMAIN": plog.SeverityNumberInfo,
		"SERVFAIL": plog.SeverityNumberWarn,
		"REFUSED":  plog.SeverityNumberWarn,
		"NOTIMP":   plog.SeverityNumberUnspecified,
	}
	for rcode, want := range cases {
		h := newHarness(t, defaultConfig())
		lr, _ := h.run(input{body: coreDNSWith(rcode)})
		if lr.SeverityNumber() != want {
			t.Errorf("%s: severity = %d, want %d", rcode, lr.SeverityNumber(), want)
		}
		if want == plog.SeverityNumberUnspecified && lr.SeverityText() != "" {
			t.Errorf("%s: severity text = %q, want empty", rcode, lr.SeverityText())
		}
	}
}

func TestConsume_CoreDNS_NoLevelPrefixAndIPv6_ShapesTheLine(t *testing.T) {
	h := newHarness(t, defaultConfig())
	line := `[2001:db8::7]:5353 - 4 "AAAA IN example.org. tcp 41 true 65535" NOERROR qr,rd,ra 98 0.0123s`

	lr, _ := h.run(input{body: line})

	if got := lr.Body().Str(); got != "NOERROR AAAA example.org. 12.3ms" {
		t.Fatalf("body = %q", got)
	}
	if got := attrStr(t, lr.Attributes(), "client.address"); got != "2001:db8::7" {
		t.Fatalf("client.address = %q", got)
	}
}

func TestConsume_CoreDNS_OtherLines_PassThroughUnmatched(t *testing.T) {
	for _, line := range []string{
		`[INFO] plugin/reload: Running configuration SHA512 = abc`,
		`[WARNING] No files matching import glob pattern: custom/*.override`,
		`.:53`,
		`plain text`,
	} {
		h := newHarness(t, defaultConfig())
		lr, _ := h.run(input{body: line})
		if lr.Body().Str() != line {
			t.Errorf("body changed: %q", lr.Body().Str())
		}
		absent(t, lr.Attributes(), "log.record.original", "dns.question.name")
		if got := h.counter(unmatchedMetric, "source"); got["none"] != 1 {
			t.Errorf("%q: unmatched counter = %v", line, got)
		}
	}
}

func TestConsume_CoreDNS_BadNumber_CountsParseFailedAndKeepsRecord(t *testing.T) {
	h := newHarness(t, defaultConfig())
	line := `[INFO] 198.51.100.42:56818 - 1 "A IN x. udp 24 false 512" NOERROR qr 24 1.2.3s`

	lr, _ := h.run(input{body: line})

	if lr.Body().Str() != line {
		t.Errorf("body changed: %q", lr.Body().Str())
	}
	absent(t, lr.Attributes(), "log.record.original", "dns.question.name")
	if got := h.counter(parseFailedMetric, "format"); got["coredns"] != 1 || len(got) != 1 {
		t.Errorf("parse_failed = %v", got)
	}
	if got := h.counter(unmatchedMetric, "source"); len(got) != 0 {
		t.Errorf("unmatched = %v", got)
	}
}

func TestConsume_CoreDNS_Malformed_PassesThroughUnmatched(t *testing.T) {
	for name, line := range map[string]string{
		"empty":     "",
		"truncated": `[INFO] 198.51.100.42:56818 - 13677 "A IN noh`,
		"binary":    "[INFO] \x00\xff\xfe - \x01 \"\x80\"",
		"huge":      `[INFO] ` + strings.Repeat("a", 1<<20),
	} {
		h := newHarness(t, defaultConfig())
		lr, _ := h.run(input{body: line})
		if lr.Body().Str() != line {
			t.Errorf("%s: body changed", name)
		}
		absent(t, lr.Attributes(), "log.record.original", "dns.question.name")
		if got := h.counter(unmatchedMetric, "source"); got["none"] != 1 {
			t.Errorf("%s: unmatched = %v", name, got)
		}
		if got := h.counter(parseFailedMetric, "format"); len(got) != 0 {
			t.Errorf("%s: parse_failed = %v", name, got)
		}
	}
}

func TestConsume_CoreDNS_ExistingOriginal_IsKept(t *testing.T) {
	h := newHarness(t, defaultConfig())

	lr, _ := h.run(input{body: coreDNSLine, attrs: map[string]any{"log.record.original": "earlier raw line"}})

	if got := attrStr(t, lr.Attributes(), "log.record.original"); got != "earlier raw line" {
		t.Errorf("log.record.original = %q", got)
	}
	if got := lr.Body().Str(); got != "SERVFAIL A nohost. 70us" {
		t.Errorf("body = %q", got)
	}
}
