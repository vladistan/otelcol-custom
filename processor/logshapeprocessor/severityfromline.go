// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"regexp"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"

	"github.com/vladistan/otelcol-custom/shape/severity"
)

// A level is read from a key (level, lvl, severity) with a value, or from a bracket or parenthesis
// group; the first one in the line decides, and a word outside the word table leaves the level unset.
var levelForm = regexp.MustCompile(`(?i)(?:\b(?:level|lvl|severity)=["']?([a-z]+)|[\[(]([a-z]+)[\])])`)

// A line may also start with its level, after an optional timestamp. A first word that is not a
// level word is not a level token, so the search goes on to the forms above.
var leadingWord = regexp.MustCompile(`(?i)^\s*(?:\d{4}-\d\d-\d\d[T ]\d\d:\d\d:\d\d(?:[.,]\d+)?(?:Z|[+-]\d\d:?\d\d)?\s+)?([a-z]+)\b`)

// lineLevel returns the level of the line, or an unset level when it has no level token.
func lineLevel(line string) severity.Level {
	form := levelForm.FindStringSubmatchIndex(line)
	if m := leadingWord.FindStringSubmatchIndex(line); m != nil {
		if l := wordLevel(line[m[2]:m[3]]); l.Number != 0 && (form == nil || m[2] < form[0]) {
			return l
		}
	}
	if form == nil {
		return severity.Level{}
	}
	if form[2] >= 0 {
		return wordLevel(line[form[2]:form[3]])
	}
	return wordLevel(line[form[4]:form[5]])
}

// levelFromLine gives a record of a configured service that has no severity the level of its line.
// A severity that is already set is kept. The original line is read when a branch kept one, else the body.
func (p *shapeProcessor) levelFromLine(res pcommon.Resource, lr plog.LogRecord) {
	if len(p.lineServices) == 0 || lr.SeverityNumber() != plog.SeverityNumberUnspecified {
		return
	}
	if _, ok := p.lineServices[stringAttr(res.Attributes(), serviceKey)]; !ok {
		return
	}
	line := bodyText(lr)
	if v, ok := lr.Attributes().Get(originalKey); ok && v.Type() == pcommon.ValueTypeStr && v.Str() != "" {
		line = v.Str()
	}
	if l := lineLevel(line); l.Number != 0 {
		applyLevel(lr, l)
	}
}
