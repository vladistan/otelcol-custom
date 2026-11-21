// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"regexp"
	"strings"

	"github.com/vladistan/otelcol-custom/shape/severity"
	"github.com/vladistan/otelcol-custom/shape/strip"
)

const serialHostLabel = "gcp.label.compute.googleapis.com/resource_name"

var (
	okMark     = regexp.MustCompile(`^\[\s*OK\s*\]`)
	failedMark = regexp.MustCompile(`^\[\s*FAILED\s*\]`)
	jobMark    = regexp.MustCompile(`^\[\s*\*+\s*\].*running`)
)

// serialConsoleBranch shapes GCE serial console records: the escaped text is unescaped and stripped
// of ANSI, the host comes from the instance label, and the systemd status marker gives the severity.
// A record that is only a line break keeps an empty body for a later drop.
type serialConsoleBranch struct{}

func newSerialConsoleBranch() *serialConsoleBranch { return &serialConsoleBranch{} }

func (*serialConsoleBranch) name() string { return "serialconsole" }

func (*serialConsoleBranch) claims(r *rec) bool {
	return strings.Contains(attrText(r.lr.Attributes(), "cloud.resource_id"), "serialconsole.googleapis.com") ||
		strings.Contains(attrText(r.res.Attributes(), "cloud.resource_id"), "serialconsole.googleapis.com")
}

func (*serialConsoleBranch) parse(r *rec) error {
	text := strip.SerialConsole(r.body)
	var level severity.Level
	switch {
	case okMark.MatchString(text):
		level = wordLevel("info")
	case failedMark.MatchString(text):
		level = wordLevel("error")
	case jobMark.MatchString(text):
		level = wordLevel("warn")
	}
	a := r.lr.Attributes()
	a.PutStr("log.record.original", r.body)
	if host := attrText(a, serialHostLabel); host != "" {
		r.res.Attributes().PutStr("host.name", host)
	}
	applyLevel(r.lr, level)
	r.lr.Body().SetStr(text)
	return nil
}
