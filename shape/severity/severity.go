// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

// cspell:ignore dpanic eror NOERROR NXDOMAIN

// Package severity maps the level names of each log source to one OpenTelemetry
// severity. The text always comes from the number. An unknown value stays unset.
package severity

import (
	"strconv"
	"strings"
)

// Level is a severity number with its text. The zero value is unset.
type Level struct {
	Number int32
	Text   string
}

// Source names the table a raw level value is read with.
type Source int

// The level sources of the log formats.
const (
	Word Source = iota
	Klog
	Mongod
	ClickHouse
	Postgres
	Pino
	Redis
)

var (
	trace = Level{1, "TRACE"}
	debug = Level{5, "DEBUG"}
	info  = Level{9, "INFO"}
	warn  = Level{13, "WARN"}
	erro  = Level{17, "ERROR"}
	fatal = Level{21, "FATAL"}
)

var words = map[string]Level{
	"trace": trace, "debug": debug, "info": info, "notice": info,
	"warn": warn, "warning": warn, "error": erro, "eror": erro, "err": erro,
	"fatal": fatal, "critical": fatal, "crit": fatal, "alert": fatal, "emergency": fatal, "panic": fatal, "dpanic": fatal,
}

var tables = map[Source]map[string]Level{
	Klog:       {"I": info, "W": warn, "E": erro, "F": fatal},
	Mongod:     {"I": info, "W": warn, "E": erro, "F": fatal},
	ClickHouse: {"Trace": trace, "Debug": debug, "Information": info, "Notice": info, "Warning": warn, "Error": erro, "Critical": fatal, "Fatal": fatal},
	Postgres:   {"LOG": info, "INFO": info, "NOTICE": info, "WARNING": warn, "ERROR": erro, "FATAL": fatal, "PANIC": fatal},
	Pino:       {"10": trace, "20": debug, "30": info, "40": warn, "50": erro, "60": fatal},
	Redis:      {".": debug, "-": info, "*": info, "#": warn},
}

// FromSource reads a raw level of the given source. The bool is false for an unknown value.
func FromSource(s Source, raw string) (Level, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Level{}, false
	}
	switch s {
	case Word:
		l, ok := words[strings.ToLower(raw)]
		return l, ok
	case Mongod:
		if len(raw) == 2 && raw[0] == 'D' && raw[1] >= '1' && raw[1] <= '5' {
			return debug, true
		}
	case Postgres:
		if n, ok := strings.CutPrefix(raw, "DEBUG"); ok {
			if v, err := strconv.Atoi(n); err == nil && v >= 1 && v <= 5 {
				return debug, true
			}
			if n == "" {
				return debug, true
			}
		}
	}
	l, ok := tables[s][raw]
	return l, ok
}

// FromHTTPStatus maps a status: 5xx ERROR, 4xx WARN, 1xx to 3xx INFO, anything else unset.
func FromHTTPStatus(status int) (Level, bool) {
	switch {
	case status >= 500 && status <= 599:
		return erro, true
	case status >= 400 && status <= 499:
		return warn, true
	case status >= 100 && status <= 399:
		return info, true
	}
	return Level{}, false
}

// FromDNSResponse maps a DNS response status: SERVFAIL and REFUSED WARN, NOERROR and NXDOMAIN INFO.
func FromDNSResponse(status string) (Level, bool) {
	switch strings.ToUpper(status) {
	case "SERVFAIL", "REFUSED":
		return warn, true
	case "NOERROR", "NXDOMAIN":
		return info, true
	}
	return Level{}, false
}

// FromNumber returns the level of an OpenTelemetry severity number 1 to 24, unset otherwise.
func FromNumber(n int32) Level {
	switch {
	case n >= 1 && n <= 4:
		return Level{n, "TRACE"}
	case n >= 5 && n <= 8:
		return Level{n, "DEBUG"}
	case n >= 9 && n <= 12:
		return Level{n, "INFO"}
	case n >= 13 && n <= 16:
		return Level{n, "WARN"}
	case n >= 17 && n <= 20:
		return Level{n, "ERROR"}
	case n >= 21 && n <= 24:
		return Level{n, "FATAL"}
	}
	return Level{}
}

// Pick returns the embedded level when there is one. A stream or priority hint
// alone never sets a severity.
func Pick(embedded Level, hasEmbedded bool, _ Level, _ bool) Level {
	if hasEmbedded {
		return embedded
	}
	return Level{}
}

// MergeMap returns the defaults with each override key replacing its default. The defaults are not changed.
func MergeMap(defaults, overrides map[string]Level) map[string]Level {
	out := make(map[string]Level, len(defaults)+len(overrides))
	for k, v := range defaults {
		out[k] = v
	}
	for k, v := range overrides {
		out[k] = v
	}
	return out
}
