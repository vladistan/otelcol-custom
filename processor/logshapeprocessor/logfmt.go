// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"

	"github.com/vladistan/otelcol-custom/shape/body"
)

var (
	logfmtPair = regexp.MustCompile(`([\w.\-]+)=(?:("(?:[^"\\]|\\.)*")|(\S*))`)
	logfmtHint = regexp.MustCompile(`(?:^|\s)(?:logger|component|module|lvl)=`)
)

// logfmtLifted are the keys that leave the line: they become the level, time, scope, source
// location or user, or the message itself.
var logfmtLifted = map[string]bool{
	"level": true, "lvl": true, "t": true, "ts": true, "logger": true, "component": true, "module": true,
	"msg": true, "source": true, "caller": true, "uname": true,
}

// logfmtTimeLayouts are the time forms of a logfmt line: RFC 3339 with a colon in the zone offset,
// and the same with an offset such as +0100.
var logfmtTimeLayouts = []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999999-0700"}

func parseLogfmtTime(raw string) (time.Time, error) {
	var err error
	for _, layout := range logfmtTimeLayouts {
		var ts time.Time
		if ts, err = time.Parse(layout, raw); err == nil {
			return ts, nil
		}
	}
	return time.Time{}, err
}

// logfmtBranch shapes logfmt lines of dashboards and databases: msg and level (or lvl) with t or
// ts, and logger, component or module. A line that starts with time= is a logrus text line and
// belongs to the logrus branch.
type logfmtBranch struct{}

func newLogfmtBranch() *logfmtBranch { return &logfmtBranch{} }

func (*logfmtBranch) name() string { return "logfmt" }

func (*logfmtBranch) claims(r *rec) bool {
	s := r.body
	if strings.HasPrefix(s, "time=") || !strings.Contains(s, "msg=") {
		return false
	}
	hasLevel := strings.Contains(s, "level=") || strings.Contains(s, "lvl=")
	return hasLevel && (strings.HasPrefix(s, "t=") || strings.HasPrefix(s, "ts=") || logfmtHint.MatchString(s))
}

func (*logfmtBranch) parse(r *rec) error {
	type pair struct{ key, value string }
	var pairs []pair
	for _, m := range logfmtPair.FindAllStringSubmatch(r.body, -1) {
		value := m[3]
		if m[2] != "" {
			var err error
			if value, err = strconv.Unquote(m[2]); err != nil {
				return err
			}
		}
		pairs = append(pairs, pair{m[1], value})
	}
	get := func(keys ...string) string {
		for _, k := range keys {
			for _, p := range pairs {
				if p.key == k && p.value != "" {
					return p.value
				}
			}
		}
		return ""
	}
	msg := get("msg")
	if msg == "" {
		return errors.New("logfmt line has no message")
	}
	var ts time.Time
	if raw := get("t", "ts"); raw != "" {
		var err error
		if ts, err = parseLogfmtTime(raw); err != nil {
			return err
		}
	}
	var rest []body.Pair
	for _, p := range pairs {
		if !logfmtLifted[p.key] {
			rest = append(rest, body.Pair{Key: p.key, Value: p.value})
		}
	}

	a := r.lr.Attributes()
	a.PutStr("log.record.original", r.body)
	if path, line, ok := splitSourceLocation(get("source", "caller")); ok {
		a.PutStr("code.file.path", path)
		a.PutInt("code.line.number", line)
	}
	if user := get("uname"); user != "" {
		a.PutStr("user.email", user)
	}
	if !ts.IsZero() {
		r.lr.SetTimestamp(pcommon.NewTimestampFromTime(ts.UTC()))
	}
	applyLevel(r.lr, wordLevel(get("level", "lvl")))
	r.lr.Body().SetStr(body.Compose("", msg, rest...))
	if scope := get("logger", "component", "module"); scope != "" {
		r.scope.SetName(scope)
	}
	return nil
}

// splitSourceLocation splits "file.go:12" into the file and the line number.
func splitSourceLocation(s string) (string, int64, bool) {
	i := strings.LastIndexByte(s, ':')
	if i <= 0 {
		return "", 0, false
	}
	n, err := strconv.ParseInt(s[i+1:], 10, 64)
	if err != nil {
		return "", 0, false
	}
	return s[:i], n, true
}
