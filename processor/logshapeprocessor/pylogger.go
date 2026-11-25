// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"errors"
	"regexp"
	"strings"
)

// pyloggerLine reads the first line of a Python logging record in the default format: a level token, a colon
// with no space, a logger of letters, digits, "_" and "." and a second colon. The logger ends at that colon.
// A uvicorn line (a colon, then spaces) has no logger and does not match.
var pyloggerLine = regexp.MustCompile(`^(DEBUG|INFO|WARNING|WARN|ERROR|CRITICAL|FATAL):([A-Za-z0-9_.]+):(.*)$`)

type pyloggerInfo struct{ level, logger, msg string }

func parsePyLogger(firstLine string) (*pyloggerInfo, bool) {
	m := pyloggerLine.FindStringSubmatch(strings.TrimSuffix(firstLine, "\r"))
	if m == nil {
		return nil, false
	}
	return &pyloggerInfo{level: m[1], logger: m[2], msg: strings.TrimSpace(m[3])}, true
}

// pyloggerBranch shapes Python logging lines. A traceback is claimed by its first line: logrecombine merges
// the lines after it, and they stay in log.record.original.
type pyloggerBranch struct{}

func newPyLoggerBranch() *pyloggerBranch { return &pyloggerBranch{} }

func (*pyloggerBranch) name() string { return "pylogger" }

func (*pyloggerBranch) claims(r *rec) bool {
	first, _, _ := strings.Cut(r.body, "\n")
	info, ok := parsePyLogger(first)
	if ok {
		r.memo = info
	}
	return ok
}

func (*pyloggerBranch) parse(r *rec) error {
	info := memoOf[pyloggerInfo](r)
	if info == nil {
		first, _, _ := strings.Cut(r.body, "\n")
		var ok bool
		if info, ok = parsePyLogger(first); !ok {
			return errors.New("not a Python logging line")
		}
	}
	r.lr.Attributes().PutStr(originalKey, r.body)
	applyLevel(r.lr, wordLevel(info.level))
	body := "[" + info.logger + "]"
	if info.msg != "" {
		body += " " + info.msg
	}
	r.lr.Body().SetStr(body)
	r.scope.SetName(info.logger)
	return nil
}
