// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

// cspell:ignore calico felix

package logshapeprocessor

import (
	"errors"
	"regexp"
	"strconv"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
)

var calicoShape = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}\.\d{3}) \[(\w+)\]\[(\d+)\] (\S+) (\d+): (.*)$`)

// calicoBranch shapes calico node lines: "time [LEVEL][thread] file line: text", in UTC.
type calicoBranch struct{}

func newCalicoBranch() *calicoBranch { return &calicoBranch{} }

func (*calicoBranch) name() string { return "calico" }

func (*calicoBranch) claims(r *rec) bool { return calicoShape.MatchString(r.body) }

func (*calicoBranch) parse(r *rec) error {
	m := calicoShape.FindStringSubmatch(r.body)
	if m == nil {
		return errors.New("not a calico line")
	}
	ts, err := time.Parse("2006-01-02 15:04:05.000", m[1])
	if err != nil {
		return err
	}
	thread, _ := strconv.ParseInt(m[3], 10, 64)
	line, _ := strconv.ParseInt(m[5], 10, 64)

	a := r.lr.Attributes()
	a.PutStr("log.record.original", r.body)
	a.PutInt("thread.id", thread)
	a.PutStr("code.file.path", m[4])
	a.PutInt("code.line.number", line)
	r.lr.SetTimestamp(pcommon.NewTimestampFromTime(ts.UTC()))
	applyLevel(r.lr, wordLevel(m[2]))
	r.lr.Body().SetStr(m[6])
	return nil
}
