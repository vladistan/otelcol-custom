// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logrecombineprocessor

import (
	"strings"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"

	"github.com/vladistan/otelcol-custom/shape/multiline"
	"github.com/vladistan/otelcol-custom/shape/severity"
	"github.com/vladistan/otelcol-custom/shape/strip"
)

// clean strips ANSI codes from the text of a record and reports whether the record is blank and
// must be dropped. The text is a plain string body, or the combine field of a map body. An empty
// text is not blank: it passes unchanged.
func (p *logsProcessor) clean(record plog.LogRecord) (drop bool) {
	if !p.config.StripANSI && !p.config.DropBlank {
		return false
	}
	if record.Body().Type() == pcommon.ValueTypeStr {
		text := record.Body().Str()
		clean, blank := p.cleanText(text)
		if blank {
			return true
		}
		if clean != text {
			record.Body().SetStr(clean)
		}
		return false
	}
	text := p.getFieldValue(record, p.combineField)
	clean, blank := p.cleanText(text)
	if blank {
		return true
	}
	if clean != text {
		p.setFieldValue(record, p.combineField, clean)
	}
	return false
}

// cleanText returns the text without ANSI codes and whether it is blank. A text counts as blank
// when only white space is left after the codes go, also when it is written as escapes such as `\r\n`.
func (p *logsProcessor) cleanText(text string) (clean string, blank bool) {
	if text == "" {
		return text, false
	}
	clean = text
	if p.config.StripANSI {
		clean = strip.ANSI(text)
	}
	if p.config.DropBlank && strip.IsBlank(strip.ANSI(strip.Unescape(text))) {
		return clean, true
	}
	return clean, false
}

// markException makes a record that holds a stack trace an ERROR record, with the exception type
// and message of the last line and the whole text as the stack trace.
func markException(record plog.LogRecord, text string) {
	start := -1
	offset := 0
	for _, line := range strings.Split(text, "\n") {
		if multiline.IsStart(line) {
			start = offset
			break
		}
		offset += len(line) + 1
	}
	if start < 0 {
		return
	}
	level, _ := severity.FromSource(severity.Word, "error")
	record.SetSeverityNumber(plog.SeverityNumber(level.Number))
	record.SetSeverityText(level.Text)
	a := record.Attributes()
	if typ, msg, ok := multiline.SplitException(text[start:]); ok {
		a.PutStr("exception.type", typ)
		a.PutStr("exception.message", msg)
	}
	a.PutStr("exception.stacktrace", text)
}
