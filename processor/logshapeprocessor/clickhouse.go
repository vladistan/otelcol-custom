// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

// cspell:ignore vstan

package logshapeprocessor

import (
	"errors"
	"regexp"
	"strconv"
	"strings"

	"github.com/vladistan/otelcol-custom/shape/severity"
)

var clickhouseShape = regexp.MustCompile(`(?s)^(\d{4}\.\d{2}\.\d{2} \d{2}:\d{2}:\d{2}\.\d+) \[ *(\d+) *\] \{([^}]*)\} <(\w+)> ([^:\n]+): (.*)$`)

// clickhouseStackMark starts the stack trace block that ClickHouse appends to an error message.
const clickhouseStackMark = "Stack trace (when copying this message"

// clickhouseBranch shapes ClickHouse server lines: "date [ thread ] {query id} <Level> Logger: text".
// The line has no time zone, so the record time stays as it came.
type clickhouseBranch struct{}

func newClickHouseBranch() *clickhouseBranch { return &clickhouseBranch{} }

func (*clickhouseBranch) name() string { return "clickhouse" }

func (*clickhouseBranch) claims(r *rec) bool { return clickhouseShape.MatchString(r.body) }

func (*clickhouseBranch) parse(r *rec) error {
	m := clickhouseShape.FindStringSubmatch(r.body)
	if m == nil {
		return errors.New("not a ClickHouse line")
	}
	thread, err := strconv.ParseInt(m[2], 10, 64)
	if err != nil {
		return err
	}
	text := m[6]
	if i := strings.Index(text, clickhouseStackMark); i >= 0 {
		text = text[:i]
	}
	level, _ := severity.FromSource(severity.ClickHouse, m[4])

	a := r.lr.Attributes()
	a.PutStr("log.record.original", r.body)
	a.PutInt("thread.id", thread)
	if m[3] != "" {
		a.PutStr("vstan.clickhouse.query_id", m[3])
	}
	applyLevel(r.lr, level)
	r.lr.Body().SetStr(strings.TrimSpace(text))
	r.scope.SetName(m[5])
	return nil
}
