// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package winlogprocessor

import (
	"strconv"
	"strings"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
)

// auditFailureKeyword is the Windows keyword bit for "Audit Failure" events.
const auditFailureKeyword uint64 = 0x10000000000000

// levelSeverity maps a Windows event level to an OTLP severity.
func levelSeverity(level int) (plog.SeverityNumber, string, bool) {
	switch level {
	case 1:
		return plog.SeverityNumberFatal, "FATAL", true
	case 2:
		return plog.SeverityNumberError, "ERROR", true
	case 3:
		return plog.SeverityNumberWarn, "WARN", true
	case 4, 0:
		return plog.SeverityNumberInfo, "INFO", true
	case 5:
		return plog.SeverityNumberDebug, "DEBUG", true
	}
	return plog.SeverityNumberUnspecified, "", false
}

// severity returns the record severity: WARN for an Audit Failure, otherwise
// the one mapped from the level. ok is false when neither is known.
func (f *eventFields) severity() (plog.SeverityNumber, string, bool) {
	if f.hasAuditFailure() {
		return plog.SeverityNumberWarn, "WARN", true
	}
	level := f.eventLevel()
	if level < 0 {
		return plog.SeverityNumberUnspecified, "", false
	}
	return levelSeverity(level)
}

// isRawDigits reports whether s is a bare number such as "5", the form a raw
// level takes when the receiver copies it into the severity text.
func isRawDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func (f *eventFields) hasAuditFailure() bool {
	if orig, ok := f.rec.Attributes().Get(logRecordOriginalKey); ok && orig.Type() == pcommon.ValueTypeStr {
		if mask, ok := xmlKeywords(orig.Str()); ok {
			return mask&auditFailureKeyword != 0
		}
	}
	if f.rec.Body().Type() != pcommon.ValueTypeMap {
		return false
	}
	keywords, ok := f.rec.Body().Map().Get(keywordsKey)
	if !ok || keywords.Type() != pcommon.ValueTypeSlice {
		return false
	}
	for i := 0; i < keywords.Slice().Len(); i++ {
		if strings.EqualFold(keywords.Slice().At(i).AsString(), "Audit Failure") {
			return true
		}
	}
	return false
}

// xmlKeywords reads the first <Keywords>0x...</Keywords>, the System keyword mask.
func xmlKeywords(xml string) (uint64, bool) {
	const open, closing = "<Keywords>", "</Keywords>"
	start := strings.Index(xml, open)
	if start < 0 {
		return 0, false
	}
	rest := xml[start+len(open):]
	end := strings.Index(rest, closing)
	if end < 0 {
		return 0, false
	}
	text := strings.TrimSpace(rest[:end])
	text = strings.TrimPrefix(strings.TrimPrefix(text, "0x"), "0X")
	mask, err := strconv.ParseUint(text, 16, 64)
	if err != nil {
		return 0, false
	}
	return mask, true
}
