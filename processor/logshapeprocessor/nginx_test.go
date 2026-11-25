// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"testing"
	"time"

	"go.opentelemetry.io/collector/pdata/plog"
)

const nginxLine = `198.51.100.40 - - [20/Nov/2026:00:26:38 +0000] "POST /api/7/envelope/ HTTP/1.1" 200 41 "-" "sentry.python/2.35.0" "198.51.100.50, 203.0.113.40"`

func TestNginx_AccessLine_GivesTheBodyTheClientTheProxyAndTheTime(t *testing.T) {
	h := newHarness(t, defaultConfig())

	lr, res := h.run(input{body: nginxLine, ts: recordTime})

	if lr.Body().Str() != "POST /api/7/envelope/ 200 41B" {
		t.Errorf("body = %q", lr.Body().Str())
	}
	a := lr.Attributes()
	for k, want := range map[string]string{
		"http.request.method": "POST", "url.path": "/api/7/envelope/", "client.address": "198.51.100.50",
		"network.peer.address": "198.51.100.40", "user_agent.original": "sentry.python/2.35.0",
		"log.record.original": nginxLine, "network.protocol.name": "http", "network.protocol.version": "1.1",
	} {
		if got := attrStr(t, a, k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	attrInt(t, a, "http.response.status_code", 200)
	attrInt(t, a, "http.response.body.size", 41)
	absent(t, a, "network.forwarded_for", "original_message", "http.response.body.bytes")
	if lr.SeverityNumber() != plog.SeverityNumberInfo || lr.SeverityText() != "INFO" {
		t.Errorf("severity = %d %q", lr.SeverityNumber(), lr.SeverityText())
	}
	if want := time.Date(2026, 11, 20, 0, 26, 38, 0, time.UTC); !lr.Timestamp().AsTime().Equal(want) {
		t.Errorf("time = %v, want %v", lr.Timestamp().AsTime(), want)
	}
	if attrStr(t, res.Attributes(), "service.name") != "nginx" {
		t.Errorf("resource = %v", res.Attributes().AsRaw())
	}
}

func TestNginx_NoForwardedEntry_TakesThePeerAsTheClient(t *testing.T) {
	h := newHarness(t, defaultConfig())

	lr, _ := h.run(input{body: `10.0.0.5 - - [20/Nov/2026:00:26:38 +0000] "GET /healthz HTTP/1.1" 200 2 "-" "kube-probe/1.36" "-"`})

	if attrStr(t, lr.Attributes(), "client.address") != "10.0.0.5" {
		t.Errorf("attributes = %v", lr.Attributes().AsRaw())
	}
	absent(t, lr.Attributes(), "network.peer.address")
	if lr.Body().Str() != "GET /healthz 200 2B" {
		t.Errorf("body = %q", lr.Body().Str())
	}
}

func TestNginx_ForwardedEntryWithAPort_GivesTheAddress(t *testing.T) {
	h := newHarness(t, defaultConfig())

	lr, _ := h.run(input{body: `192.0.2.84 - - [31/Dec/2025:00:30:00 +0000] "POST /api/2/envelope/ HTTP/1.1" 200 41 "-" "sentry.go/0.40.0" "192.0.2.85:46331"`})

	if attrStr(t, lr.Attributes(), "client.address") != "192.0.2.85" || attrStr(t, lr.Attributes(), "network.peer.address") != "192.0.2.84" {
		t.Errorf("attributes = %v", lr.Attributes().AsRaw())
	}
}

func TestNginx_StatusRule_FiveHundredErrorAndFourHundredWarn(t *testing.T) {
	h := newHarness(t, defaultConfig())
	for _, c := range []struct {
		status string
		num    plog.SeverityNumber
		text   string
	}{
		{"502", plog.SeverityNumberError, "ERROR"},
		{"403", plog.SeverityNumberWarn, "WARN"},
		{"301", plog.SeverityNumberInfo, "INFO"},
	} {
		line := `10.0.0.5 - - [20/Nov/2026:00:26:38 +0000] "GET /x HTTP/1.1" ` + c.status + ` 0 "-" "curl/8" "-"`
		lr, _ := h.run(input{body: line})
		if lr.SeverityNumber() != c.num || lr.SeverityText() != c.text {
			t.Errorf("%s: %d %q, want %d %q", c.status, lr.SeverityNumber(), lr.SeverityText(), c.num, c.text)
		}
	}
}

func TestNginx_QueryIsAPartOfTheBodyAndAnAttribute(t *testing.T) {
	h := newHarness(t, defaultConfig())

	lr, _ := h.run(input{body: `10.0.0.5 - - [20/Nov/2026:00:26:38 +0000] "GET /search?q=a&b=1 HTTP/1.1" 200 5 "-" "curl/8" "-"`})

	if lr.Body().Str() != "GET /search q=a&b=1 200 5B" || attrStr(t, lr.Attributes(), "url.query") != "q=a&b=1" {
		t.Errorf("body %q attributes %v", lr.Body().Str(), lr.Attributes().AsRaw())
	}
}

func TestNginx_OSOfTheBrowser_IsUserAgentOSNameNotTheHostOS(t *testing.T) {
	h := newHarness(t, defaultConfig())

	lr, _ := h.run(input{body: `198.51.100.9 - - [20/Nov/2026:00:26:41 +0000] "GET / HTTP/1.1" 200 5 "-" "Mozilla/5.0 (X11; Linux x86_64) Gecko/20100101 Firefox/120.0" "-"`})

	if attrStr(t, lr.Attributes(), "user_agent.os.name") != "Linux" {
		t.Errorf("attributes = %v", lr.Attributes().AsRaw())
	}
	absent(t, lr.Attributes(), "os.name")
}

func TestNginx_RefererAndRemoteUser_AreKept(t *testing.T) {
	h := newHarness(t, defaultConfig())

	lr, _ := h.run(input{body: `10.0.0.5 - alice [20/Nov/2026:00:26:38 +0000] "GET / HTTP/1.1" 200 5 "https://example.org/a" "curl/8" "-"`})

	if attrStr(t, lr.Attributes(), "http.request.header.referer") != "https://example.org/a" || attrStr(t, lr.Attributes(), "user.name") != "alice" {
		t.Errorf("attributes = %v", lr.Attributes().AsRaw())
	}
}

func TestNginx_ParseTimestampOff_KeepsTheRecordTime(t *testing.T) {
	cfg := defaultConfig()
	cfg.Nginx.ParseTimestamp = false
	h := newHarness(t, cfg)

	lr, _ := h.run(input{body: nginxLine, ts: recordTime})

	if !lr.Timestamp().AsTime().Equal(recordTime) {
		t.Errorf("time = %v", lr.Timestamp().AsTime())
	}
}

func TestNginx_ServiceName_KeptWhenSetAndSkippedWhenOff(t *testing.T) {
	h := newHarness(t, defaultConfig())
	_, res := h.run(input{body: nginxLine, resource: map[string]any{"service.name": "tea-frontend"}})
	if attrStr(t, res.Attributes(), "service.name") != "tea-frontend" {
		t.Errorf("resource = %v", res.Attributes().AsRaw())
	}

	cfg := defaultConfig()
	cfg.Nginx.SetServiceName = false
	h = newHarness(t, cfg)
	_, res = h.run(input{body: nginxLine})
	absent(t, res.Attributes(), "service.name")
}

func TestNginx_InfluxHTTPDLine_IsNotClaimedByNginx(t *testing.T) {
	cfg := defaultConfig()
	cfg.Branches = []string{"nginx"}
	h := newHarness(t, cfg)
	const line = `[httpd] 192.0.2.19 - mainflux [20/Nov/2026:00:02:45 +0100] "POST /write?db=zero HTTP/1.1" 204 0 "-" "InfluxDBClient" 5a1b2c3d 14684`

	lr, _ := h.run(input{body: line})

	if lr.Body().Str() != line {
		t.Errorf("body = %q", lr.Body().Str())
	}
	if got := h.counter(parseFailedMetric, "format"); got["nginx"] != 0 {
		t.Errorf("parse_failed = %v, want none", got)
	}
	if got := h.counter(unmatchedMetric, "source"); got["none"] != 1 {
		t.Errorf("unmatched = %v", got)
	}
}

func TestNginx_ErrorLine_IsNotClaimed(t *testing.T) {
	h := newHarness(t, defaultConfig())

	lr, _ := h.run(input{body: "2026/11/20 00:26:38 [error] 31#31: *5 upstream timed out"})

	if lr.Attributes().Len() != 0 {
		t.Errorf("attributes = %v", lr.Attributes().AsRaw())
	}
}

func TestNginx_ForwardedField_ClientAndPeerRules(t *testing.T) {
	const tail = ` - - [20/Nov/2026:00:26:38 +0000] "GET /a HTTP/1.1" 200 5 "-" "curl/8.0"`
	cases := []struct {
		name, xff, client, peer string
	}{
		{"first of several hops", ` "203.0.113.7, 198.51.100.2, 192.0.2.9"`, "203.0.113.7", "192.0.2.1"},
		{"single entry", ` "203.0.113.7"`, "203.0.113.7", "192.0.2.1"},
		{"entry with a port", ` "203.0.113.7:51000"`, "203.0.113.7", "192.0.2.1"},
		{"dash", ` "-"`, "192.0.2.1", ""},
		{"empty", ` ""`, "192.0.2.1", ""},
		{"field missing", ``, "192.0.2.1", ""},
		{"unknown first entry", ` "unknown, 203.0.113.7"`, "192.0.2.1", ""},
		{"hostname first entry", ` "edge.example.com"`, "192.0.2.1", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t, defaultConfig())

			lr, _ := h.run(input{body: "192.0.2.1" + tail + c.xff})

			a := lr.Attributes()
			if got := attrStr(t, a, "client.address"); got != c.client {
				t.Errorf("client.address = %q, want %q", got, c.client)
			}
			if c.peer == "" {
				absent(t, a, "network.peer.address", "network.forwarded_for")
			} else if got := attrStr(t, a, "network.peer.address"); got != c.peer {
				t.Errorf("network.peer.address = %q, want %q", got, c.peer)
			}
		})
	}
}
