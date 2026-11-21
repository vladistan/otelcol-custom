// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"strconv"
	"strings"
	"unicode"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"

	"github.com/vladistan/otelcol-custom/shape/severity"
)

// applyLevel writes a level to the record; an unset level clears any earlier default.
func applyLevel(lr plog.LogRecord, l severity.Level) {
	lr.SetSeverityNumber(plog.SeverityNumber(l.Number))
	lr.SetSeverityText(l.Text)
}

// wordLevel reads a level word (info, warning, error ...); an unknown word is unset.
func wordLevel(word string) severity.Level {
	l, _ := severity.FromSource(severity.Word, word)
	return l
}

// putScalar writes a value as an integer when it is one, otherwise as a string.
func putScalar(m pcommon.Map, key, value string) {
	if n, err := strconv.ParseInt(value, 10, 64); err == nil {
		m.PutInt(key, n)
		return
	}
	m.PutStr(key, value)
}

// underscoreCase turns StallMinutes or connectionId into stall_minutes or connection_id.
func underscoreCase(s string) string {
	var b strings.Builder
	for i, c := range s {
		if unicode.IsUpper(c) && i > 0 {
			b.WriteByte('_')
		}
		b.WriteRune(unicode.ToLower(c))
	}
	return b.String()
}
