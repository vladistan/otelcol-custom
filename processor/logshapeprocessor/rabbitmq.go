// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

// cspell:ignore vstan

package logshapeprocessor

import (
	"errors"
	"regexp"
	"strings"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"

	"github.com/vladistan/otelcol-custom/shape/strip"
)

var rabbitShape = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}\.\d+[+-]\d{2}:\d{2}) \[(\w+)\] (?:(<[\d.]+>) )?(.*)$`)

// rabbitmqBranch shapes rabbitmq lines: time, [level], an Erlang pid and the message, with ANSI colour codes.
type rabbitmqBranch struct{}

func newRabbitMQBranch() *rabbitmqBranch { return &rabbitmqBranch{} }

func (*rabbitmqBranch) name() string { return "rabbitmq" }

func (*rabbitmqBranch) claims(r *rec) bool {
	return strings.Contains(r.body, " [") && rabbitShape.MatchString(strip.ANSI(r.body))
}

func (*rabbitmqBranch) parse(r *rec) error {
	m := rabbitShape.FindStringSubmatch(strip.ANSI(r.body))
	if m == nil {
		return errors.New("not a rabbitmq line")
	}
	ts, err := time.Parse("2006-01-02 15:04:05.999999999-07:00", m[1])
	if err != nil {
		return err
	}
	a := r.lr.Attributes()
	a.PutStr("log.record.original", r.body)
	if m[3] != "" {
		a.PutStr("vstan.rabbitmq.pid", m[3])
	}
	r.lr.SetTimestamp(pcommon.NewTimestampFromTime(ts.UTC()))
	applyLevel(r.lr, wordLevel(m[2]))
	r.lr.Body().SetStr(strings.TrimSpace(m[4]))
	return nil
}
