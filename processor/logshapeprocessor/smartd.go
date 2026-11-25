// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"errors"
	"regexp"
	"strings"

	"github.com/vladistan/otelcol-custom/shape/severity"
)

var (
	smartdShape = regexp.MustCompile(`^Device: (/dev/\S+)(?: \[[^\]]+\])?, (.*)$`)
	smartdWarn  = regexp.MustCompile(`(?i)uncorrectable sectors|unreadable \(pending\) sectors`)
)

// smartdBranch gives smartd lines a severity: raw syslog lost the priority, so unreadable and
// uncorrectable sectors are WARN, a FAILED health check is ERROR and any other line is unset.
type smartdBranch struct{}

func newSmartdBranch() *smartdBranch { return &smartdBranch{} }

func (*smartdBranch) name() string { return "smartd" }

func (*smartdBranch) claims(r *rec) bool {
	return strings.HasPrefix(r.body, "Device: ") && smartdShape.MatchString(r.body)
}

func (*smartdBranch) parse(r *rec) error {
	m := smartdShape.FindStringSubmatch(r.body)
	if m == nil {
		return errors.New("not a smartd line")
	}
	var level severity.Level
	switch {
	case strings.Contains(m[2], "FAILED"):
		level = wordLevel("error")
	case smartdWarn.MatchString(m[2]):
		level = wordLevel("warn")
	}
	a := r.lr.Attributes()
	keepOriginal(a, r.body)
	a.PutStr("vstan.smartd.device", m[1])
	applyLevel(r.lr, level)
	return nil
}
