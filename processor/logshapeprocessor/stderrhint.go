// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"strings"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"

	"github.com/vladistan/otelcol-custom/shape/severity"
)

// stderrHintBranch is the last branch of the table. The journald receiver maps a severity from a
// priority it does not keep, and the docker stream of stderr arrives as ERROR. A journal record of a
// container with that severity, that no earlier branch found a level in, has no level of its own: a
// stderr hint alone never means an error, so the severity is cleared.
type stderrHintBranch struct{}

func newStderrHintBranch() *stderrHintBranch { return &stderrHintBranch{} }

func (*stderrHintBranch) name() string { return "stderrhint" }

func (*stderrHintBranch) claims(r *rec) bool {
	return r.lr.SeverityNumber() == plog.SeverityNumberError &&
		sourceOf(r.lr, r.res) == "journal" && inContainer(r.res.Attributes())
}

func (*stderrHintBranch) parse(r *rec) error {
	applyLevel(r.lr, severity.Level{})
	return nil
}

// inContainer reports whether the resource names a container.
func inContainer(res pcommon.Map) bool {
	found := false
	res.Range(func(k string, _ pcommon.Value) bool {
		found = strings.HasPrefix(k, "container.")
		return !found
	})
	return found
}
