// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package httpaccess

import (
	"testing"
	"time"
)

func TestParseNginx_CombinedWithForwardedFor(t *testing.T) {
	const line = `198.51.100.40 - - [20/Nov/2026:00:26:38 +0000] "POST /api/7/envelope/ HTTP/1.1" 200 41 "-" "sentry.python/2.35.0" "198.51.100.50, 203.0.113.40"`
	e, ok := ParseNginx(line)
	if !ok {
		t.Fatal("not parsed")
	}
	if e.Method != "POST" || e.Path != "/api/7/envelope/" || e.Status != 200 || e.SizeBytes != 41 || e.UserAgent != "sentry.python/2.35.0" {
		t.Fatalf("got %+v", e)
	}
	if e.ClientAddress != "198.51.100.50" || e.PeerAddress != "198.51.100.40" {
		t.Fatalf("client %q peer %q", e.ClientAddress, e.PeerAddress)
	}
	want := time.Date(2026, 11, 20, 0, 26, 38, 0, time.UTC)
	if !e.Time.Equal(want) {
		t.Fatalf("time %v, want %v", e.Time, want)
	}
}

func TestParseNginx_NoForwardedEntry_PeerIsTheClient(t *testing.T) {
	const line = `10.0.0.5 - - [20/Nov/2026:00:26:38 +0000] "GET /healthz HTTP/1.1" 200 2 "-" "kube-probe/1.36" "-"`
	e, ok := ParseNginx(line)
	if !ok || e.ClientAddress != "10.0.0.5" || e.PeerAddress != "" {
		t.Fatalf("got %+v %v", e, ok)
	}
}

func TestParseNginx_DashSizeIsZero_AndQueryIsSplit(t *testing.T) {
	const line = `10.0.0.5 - - [20/Nov/2026:00:26:38 +0000] "GET /search?q=a&b=1 HTTP/1.1" 502 - "-" "curl/8" "-"`
	e, ok := ParseNginx(line)
	if !ok || e.SizeBytes != 0 || e.Path != "/search" || e.Query != "q=a&b=1" || e.Status != 502 {
		t.Fatalf("got %+v %v", e, ok)
	}
}

func TestParseNginx_NotAnAccessLine(t *testing.T) {
	for _, in := range []string{"", "2026/11/20 00:26:38 [error] 31#31: upstream timed out", "plain text"} {
		if _, ok := ParseNginx(in); ok {
			t.Errorf("%q must not parse", in)
		}
	}
}

func TestParseInflux_HTTPDLine(t *testing.T) {
	const line = `[httpd] 192.0.2.19 - mainflux [20/Nov/2026:00:02:45 +0100] "POST /write?db=zero HTTP/1.1" 204 0 "-" "InfluxDBClient" 5a1b2c3d-1111-2222-3333-444455556666 14684`
	e, ok := ParseInflux(line)
	if !ok {
		t.Fatal("not parsed")
	}
	if e.Method != "POST" || e.Path != "/write" || e.Query != "db=zero" || e.Status != 204 || e.User != "mainflux" || e.ClientAddress != "192.0.2.19" {
		t.Fatalf("got %+v", e)
	}
	if e.DurationMicros != 14684 || !e.HasDuration {
		t.Fatalf("duration %d %v", e.DurationMicros, e.HasDuration)
	}
	if e.RequestID != "5a1b2c3d-1111-2222-3333-444455556666" {
		t.Fatalf("request id %q", e.RequestID)
	}
	want := time.Date(2026, 11, 19, 23, 2, 45, 0, time.UTC)
	if !e.Time.UTC().Equal(want) {
		t.Fatalf("time %v, want %v", e.Time.UTC(), want)
	}
}

func TestParseOAuth2Proxy_Line(t *testing.T) {
	const line = `198.51.100.135 - 7f1e2d3c-aaaa-bbbb-cccc-ddddeeeeffff - - [2026/11/19 22:28:55] 198.51.100.135:4180 GET - "/ready" HTTP/1.1 "kube-probe/1.36" 200 2 0.000`
	e, ok := ParseOAuth2Proxy(line)
	if !ok {
		t.Fatal("not parsed")
	}
	if e.Method != "GET" || e.Path != "/ready" || e.Status != 200 || e.SizeBytes != 2 || e.UserAgent != "kube-probe/1.36" {
		t.Fatalf("got %+v", e)
	}
	if e.RequestID != "7f1e2d3c-aaaa-bbbb-cccc-ddddeeeeffff" || !e.HasDuration || e.DurationMicros != 0 {
		t.Fatalf("request id %q duration %d %v", e.RequestID, e.DurationMicros, e.HasDuration)
	}
	if e.ClientAddress != "198.51.100.135" {
		t.Fatalf("client %q", e.ClientAddress)
	}
}

func TestParseUvicorn_Line(t *testing.T) {
	e, ok := ParseUvicorn(`198.51.100.145:12610 - "GET /healthz HTTP/1.1" 200 OK`)
	if !ok {
		t.Fatal("not parsed")
	}
	if e.Method != "GET" || e.Path != "/healthz" || e.Status != 200 || e.ClientAddress != "198.51.100.145" || e.ClientPort != 12610 {
		t.Fatalf("got %+v", e)
	}
	if _, ok := ParseUvicorn("not an access line"); ok {
		t.Fatal("must not parse")
	}
}

func TestFirstForwarded_TakesTheFirstEntry(t *testing.T) {
	for in, want := range map[string]string{
		"198.51.100.50, 203.0.113.40": "198.51.100.50",
		"198.51.100.50":               "198.51.100.50",
		"-":                           "",
		"":                            "",
		" 1.2.3.4 ,5.6.7.8":           "1.2.3.4",
		"unknown, 1.2.3.4":            "",
		"192.0.2.84:46331":            "192.0.2.84",
		"[2001:db8::1]:443, 1.2.3.4":  "2001:db8::1",
		"2001:db8::1":                 "2001:db8::1",
	} {
		if got := FirstForwarded(in); got != want {
			t.Errorf("FirstForwarded(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestEntry_BodyUsesTheSharedFormat(t *testing.T) {
	e := Entry{Method: "post", Path: "/write", Query: "db=zero", Status: 204, DurationMicros: 14684, HasDuration: true}
	if got := e.Body(); got != "POST /write db=zero 204 14.7ms" {
		t.Fatalf("got %q", got)
	}
	e = Entry{Method: "POST", Path: "/api/7/envelope/", Status: 200, SizeBytes: 41}
	if got := e.Body(); got != "POST /api/7/envelope/ 200 41B" {
		t.Fatalf("got %q", got)
	}
}
