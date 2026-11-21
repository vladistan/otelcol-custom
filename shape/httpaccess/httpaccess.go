// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

// Package httpaccess reads access log lines (nginx, Influx httpd, oauth2-proxy, uvicorn) into one Entry.
package httpaccess

import (
	"math"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/vladistan/otelcol-custom/shape/body"
)

// Entry is one parsed access line. Zero fields were not in the line.
type Entry struct {
	Method         string
	Path           string
	Query          string
	Status         int
	SizeBytes      int64
	DurationMicros int64
	HasDuration    bool
	ClientAddress  string
	ClientPort     int64
	PeerAddress    string
	UserAgent      string
	User           string
	RequestID      string
	Time           time.Time
}

// Body is the shared "METHOD path status size|duration" line.
func (e Entry) Body() string {
	return body.HTTP(body.HTTPLine{
		Method: e.Method, Path: e.Path, Query: e.Query, Status: e.Status,
		SizeBytes: e.SizeBytes, DurationMicros: e.DurationMicros, HasDuration: e.HasDuration,
	})
}

// FirstForwarded returns the first X-Forwarded-For entry when it is an IP address, else "".
func FirstForwarded(xff string) string {
	first, _, _ := strings.Cut(xff, ",")
	first = strings.TrimSpace(first)
	if net.ParseIP(first) != nil {
		return first
	}
	if host, _, err := net.SplitHostPort(first); err == nil && net.ParseIP(host) != nil {
		return host
	}
	return ""
}

func dash(s string) string {
	if s == "-" {
		return ""
	}
	return s
}

func splitTarget(target string) (path, query string) {
	path, query, _ = strings.Cut(target, "?")
	return path, query
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

func atoi64(s string) int64 {
	n, _ := strconv.ParseInt(s, 10, 64)
	return n
}

func hostPort(s string) (host string, port int64) {
	if h, p, err := net.SplitHostPort(s); err == nil {
		return h, atoi64(p)
	}
	return strings.Trim(s, "[]"), 0
}

const commonTime = "02/Jan/2006:15:04:05 -0700"

var nginxLine = regexp.MustCompile(`^(\S+) \S+ (\S+) \[([^\]]+)\] "(\S+) (\S+) [^"]*" (\d{3}) (\d+|-) "[^"]*" "([^"]*)"(?: "([^"]*)")?$`)

// ParseNginx reads the combined log format with an optional trailing X-Forwarded-For field.
func ParseNginx(line string) (Entry, bool) {
	m := nginxLine.FindStringSubmatch(strings.TrimSpace(line))
	if m == nil {
		return Entry{}, false
	}
	t, err := time.Parse(commonTime, m[3])
	if err != nil {
		return Entry{}, false
	}
	e := Entry{Method: m[4], Status: atoi(m[6]), SizeBytes: atoi64(m[7]), UserAgent: m[8], User: dash(m[2]), Time: t}
	e.Path, e.Query = splitTarget(m[5])
	if fwd := FirstForwarded(m[9]); fwd != "" {
		e.ClientAddress, e.PeerAddress = fwd, m[1]
	} else {
		e.ClientAddress = m[1]
	}
	return e, true
}

var influxLine = regexp.MustCompile(`^\[httpd\] (\S+) - (\S+) \[([^\]]+)\] "(\S+) (\S+) [^"]*" (\d{3}) (\d+|-) "[^"]*" "([^"]*)" (\S+) (\d+)$`)

// ParseInflux reads an InfluxDB httpd line; the last field is the duration in microseconds.
func ParseInflux(line string) (Entry, bool) {
	m := influxLine.FindStringSubmatch(strings.TrimSpace(line))
	if m == nil {
		return Entry{}, false
	}
	t, err := time.Parse(commonTime, m[3])
	if err != nil {
		return Entry{}, false
	}
	e := Entry{
		Method: m[4], Status: atoi(m[6]), SizeBytes: atoi64(m[7]), UserAgent: m[8], User: dash(m[2]),
		ClientAddress: m[1], RequestID: m[9], DurationMicros: atoi64(m[10]), HasDuration: true, Time: t,
	}
	e.Path, e.Query = splitTarget(m[5])
	return e, true
}

var oauthLine = regexp.MustCompile(`^(\S+) - (\S+) - - \[([^\]]+)\] \S+ (\S+) (\S+) "([^"]*)" \S+ "([^"]*)" (\d{3}) (\d+) ([0-9.]+)$`)

// ParseOAuth2Proxy reads an oauth2-proxy request line; the last field is seconds.
func ParseOAuth2Proxy(line string) (Entry, bool) {
	m := oauthLine.FindStringSubmatch(strings.TrimSpace(line))
	if m == nil {
		return Entry{}, false
	}
	t, err := time.Parse("2006/01/02 15:04:05", m[3])
	if err != nil {
		return Entry{}, false
	}
	secs, err := strconv.ParseFloat(m[10], 64)
	if err != nil {
		return Entry{}, false
	}
	e := Entry{
		Method: m[4], User: dash(m[5]), UserAgent: m[7], Status: atoi(m[8]), SizeBytes: atoi64(m[9]),
		ClientAddress: m[1], RequestID: m[2], DurationMicros: int64(math.Round(secs * 1e6)), HasDuration: true, Time: t,
	}
	e.Path, e.Query = splitTarget(m[6])
	return e, true
}

var uvicornLine = regexp.MustCompile(`^(\S+):(\d+) - "(\S+) (\S+) [^"]*" (\d{3})(?: .*)?$`)

// ParseUvicorn reads the part of a uvicorn access line after the "INFO:" prefix.
func ParseUvicorn(line string) (Entry, bool) {
	m := uvicornLine.FindStringSubmatch(strings.TrimSpace(line))
	if m == nil {
		return Entry{}, false
	}
	host, _ := hostPort(m[1] + ":" + m[2])
	e := Entry{Method: m[3], Status: atoi(m[5]), ClientAddress: host, ClientPort: atoi64(m[2])}
	e.Path, e.Query = splitTarget(m[4])
	return e, true
}
