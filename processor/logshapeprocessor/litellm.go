// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

// cspell:ignore litellm

package logshapeprocessor

import (
	"errors"
	"regexp"
	"strconv"
	"strings"

	"github.com/vladistan/otelcol-custom/shape/strip"
)

var litellmShape = regexp.MustCompile(`^(\d{2}:\d{2}:\d{2}) - ([A-Za-z][\w .]*):([A-Za-z]+): ([\w./-]+):(\d+) - (.*)$`)

// litellmBranch shapes litellm proxy lines: "time - Logger:LEVEL: file:line - text", often in colour.
// The level in the text beats a severity that came from the stream or the journal priority, and an
// unknown level leaves the severity unset. The line has a time of day only, so the record time stays.
type litellmBranch struct{}

func newLitellmBranch() *litellmBranch { return &litellmBranch{} }

func (*litellmBranch) name() string { return "litellm" }

func (*litellmBranch) claims(r *rec) bool {
	return strings.Contains(r.body, " - ") && litellmShape.MatchString(strip.ANSI(r.body))
}

func (*litellmBranch) parse(r *rec) error {
	m := litellmShape.FindStringSubmatch(strip.ANSI(r.body))
	if m == nil {
		return errors.New("not a litellm line")
	}
	line, _ := strconv.ParseInt(m[5], 10, 64)
	a := r.lr.Attributes()
	a.PutStr("log.record.original", r.body)
	a.PutStr("code.file.path", m[4])
	a.PutInt("code.line.number", line)
	applyLevel(r.lr, wordLevel(m[3]))
	r.lr.Body().SetStr(m[6])
	r.scope.SetName(m[2])
	return nil
}
