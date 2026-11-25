// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
)

// journalPriority is the journald field the gateway reads the severity from.
const journalPriority = "PRIORITY"

// mapBody is a journald map body as it was before a branch shaped the record. A branch writes a
// string body, so restore puts the shaped text back into the map, where the gateway
// journaldprocessor still finds the journal fields.
type mapBody struct {
	fields       pcommon.Map
	key          string
	severity     plog.SeverityNumber
	severityText string
}

// saveMapBody copies the map body of a record that has a text key, and returns nil for any other body.
func saveMapBody(lr plog.LogRecord) *mapBody {
	if lr.Body().Type() != pcommon.ValueTypeMap {
		return nil
	}
	key := mapTextKey(lr.Body().Map())
	if key == "" {
		return nil
	}
	m := &mapBody{fields: pcommon.NewMap(), key: key, severity: lr.SeverityNumber(), severityText: lr.SeverityText()}
	lr.Body().Map().CopyTo(m.fields)
	return m
}

// restore rebuilds the map around the shaped text, under the key the text was read from. PRIORITY
// goes when the branch changed the severity, because it no longer says what the record says.
func (m *mapBody) restore(lr plog.LogRecord) {
	if m == nil {
		return
	}
	if lr.Body().Type() == pcommon.ValueTypeStr {
		text := lr.Body().Str()
		fields := lr.Body().SetEmptyMap()
		m.fields.CopyTo(fields)
		fields.PutStr(m.key, text)
	}
	if lr.Body().Type() == pcommon.ValueTypeMap && (lr.SeverityNumber() != m.severity || lr.SeverityText() != m.severityText) {
		lr.Body().Map().Remove(journalPriority)
	}
}
