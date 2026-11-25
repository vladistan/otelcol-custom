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
)

// springbootMarker sits in every claimed line, between the pid and the thread.
const springbootMarker = " --- ["

// stacktraceKey holds the whole merged text, as logrecombine sets it; the branch sets it when it is unset.
const stacktraceKey = "exception.stacktrace"

// springbootLine reads the first line of a Spring Boot console record: date, clock, optional zone, one of
// the six levels, a numeric pid, "---", an optional application bracket (the 3.4 form), the thread bracket,
// the logger and a colon. A level outside the six or a pid that is not a number does not match.
var springbootLine = regexp.MustCompile(
	`^(\d{4}-\d{2}-\d{2})[T ](\d{2}:\d{2}:\d{2}(?:\.\d+)?)(Z|[+-]\d{2}:\d{2})?\s+` +
		`(TRACE|DEBUG|INFO|WARN|ERROR|FATAL)\s+(\d+)\s+---\s+` +
		`(?:\[([^\]]*)\]\s+)?\[([^\]]*)\]\s+([^\s:]+)\s*:\s?(.*)$`)

type springbootInfo struct {
	ts                               time.Time
	level                            string
	pid                              int64
	application, thread, logger, msg string
}

func parseSpringBoot(firstLine string) (*springbootInfo, bool) {
	m := springbootLine.FindStringSubmatch(strings.TrimSuffix(firstLine, "\r"))
	if m == nil {
		return nil, false
	}
	zone := m[3]
	if zone == "" {
		zone = "Z"
	}
	ts, err := time.Parse(time.RFC3339Nano, m[1]+"T"+m[2]+zone)
	if err != nil {
		return nil, false
	}
	pid, err := strconv.ParseInt(m[5], 10, 64)
	if err != nil {
		return nil, false
	}
	return &springbootInfo{
		ts: ts.UTC(), level: m[4], pid: pid,
		application: strings.TrimSpace(m[6]), thread: strings.TrimSpace(m[7]), logger: m[8],
		msg: strings.TrimRight(m[9], " \t"),
	}, true
}

// springbootBranch shapes Spring Boot console lines. A stack trace is claimed by its first line: the lines
// after it are merged into the record by logrecombine and stay in log.record.original. The line has the
// time, so a zone-less time is read as UTC.
type springbootBranch struct{}

func newSpringBootBranch() *springbootBranch { return &springbootBranch{} }

func (*springbootBranch) name() string { return "springboot" }

func (*springbootBranch) claims(r *rec) bool {
	first, _, _ := strings.Cut(r.body, "\n")
	if !strings.Contains(first, springbootMarker) {
		return false
	}
	info, ok := parseSpringBoot(first)
	if ok {
		r.memo = info
	}
	return ok
}

func (*springbootBranch) parse(r *rec) error {
	info := memoOf[springbootInfo](r)
	if info == nil {
		first, _, _ := strings.Cut(r.body, "\n")
		var ok bool
		if info, ok = parseSpringBoot(first); !ok {
			return errors.New("not a Spring Boot line")
		}
	}
	a := r.lr.Attributes()
	a.PutStr(originalKey, r.body)
	if strings.Contains(r.body, "\n") {
		if _, has := a.Get(stacktraceKey); !has {
			a.PutStr(stacktraceKey, r.body)
		}
	}
	a.PutInt("process.pid", info.pid)
	putNonEmpty(a, "thread.name", info.thread)
	putNonEmpty(a, "vstan.springboot.application", info.application)
	applyLevel(r.lr, wordLevel(info.level))
	r.lr.SetTimestamp(pcommon.NewTimestampFromTime(info.ts))
	body := "[" + info.logger + "]"
	if info.msg != "" {
		body += " " + info.msg
	}
	r.lr.Body().SetStr(body)
	r.scope.SetName(info.logger)
	return nil
}
