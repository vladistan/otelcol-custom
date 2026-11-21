// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"errors"
	"regexp"
	"strconv"
	"strings"

	"github.com/vladistan/otelcol-custom/shape/multiline"
	"github.com/vladistan/otelcol-custom/shape/severity"
)

var (
	gocdWrapper = regexp.MustCompile(`^jvm \d+\s*\| ?(.*)$`)
	gocdLine    = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2},\d{3})\s+(\w+)\s+\[([^\]]+)\]\s+([\w.$]+):(\d+)\s+-\s+(.*)$`)
)

// gocdBranch shapes GoCD Java wrapper output: "jvm 1 | time LEVEL [thread] Class:line - message".
// A journald batch of several lines stays one record; the first parsed line gives the attributes
// and the highest level of the batch gives the severity.
type gocdBranch struct{}

func newGoCDBranch() *gocdBranch { return &gocdBranch{} }

func (*gocdBranch) name() string { return "gocd" }

func (*gocdBranch) claims(r *rec) bool {
	if !strings.HasPrefix(r.body, "jvm ") {
		return false
	}
	first, _, _ := strings.Cut(r.body, "\n")
	return gocdWrapper.MatchString(first)
}

func (*gocdBranch) parse(r *rec) error {
	var (
		messages []string
		level    severity.Level
		parsed   bool
	)
	a := r.lr.Attributes()
	for _, line := range strings.Split(r.body, "\n") {
		inner := line
		if w := gocdWrapper.FindStringSubmatch(line); w != nil {
			inner = w[1]
		}
		m := gocdLine.FindStringSubmatch(inner)
		if m == nil {
			messages = append(messages, inner)
			continue
		}
		messages = append(messages, m[6])
		if l := wordLevel(m[2]); l.Number > level.Number {
			level = l
		}
		if !parsed {
			parsed = true
			n, _ := strconv.ParseInt(m[5], 10, 64)
			a.PutStr("thread.name", m[3])
			a.PutStr("code.function.name", m[4])
			a.PutInt("code.line.number", n)
		}
	}
	if !parsed {
		return errors.New("no GoCD log line in the record")
	}
	a.PutStr("log.record.original", r.body)
	applyLevel(r.lr, level)
	r.lr.Body().SetStr(multiline.JoinLines(messages))
	return nil
}
