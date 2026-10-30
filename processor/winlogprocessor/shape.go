// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package winlogprocessor

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
)

const (
	originalMessageKey          = "original_message"
	originalMessageTruncatedKey = "original_message.truncated"
	eventDataPrefix             = "winlog.event_data."
	keywordsKey                 = "keywords"
)

type dataField struct{ key, value string }

// shape rewrites a kept record: severity, original_message, log.source, the
// winlog.* attributes and a text body. Severity is read before the XML is cut.
func (p *winlogProcessor) shape(record plog.LogRecord, fields *eventFields) {
	if number, text, ok := fields.severity(); ok {
		record.SetSeverityNumber(number)
		record.SetSeverityText(text)
	}
	p.moveOriginal(record)

	body := record.Body().Map()
	attrs := record.Attributes()
	attrs.PutStr("log.source", "winlog:"+fields.channel)
	putHeaderAttributes(attrs, body, fields.channel)
	data := putEventData(attrs, body)

	message := stringField(body, "message")
	if message == "" {
		eventID, hasEventID := nestedInt(body, "event_id", "id")
		message = fallbackBody(nestedString(body, "provider", "name"), eventID, hasEventID, data)
	}
	record.Body().SetStr(message)
}

// putHeaderAttributes sets the winlog.* attributes that come from the event
// header, skipping values the event lacks.
func putHeaderAttributes(attrs, body pcommon.Map, channel string) {
	attrs.PutStr("winlog.channel", channel)
	putNonEmpty(attrs, "winlog.provider_name", nestedString(body, "provider", "name"))
	putNonEmpty(attrs, "winlog.provider_guid", nestedString(body, "provider", "guid"))
	putNonEmpty(attrs, "winlog.computer_name", stringField(body, "computer"))
	putNonEmpty(attrs, "winlog.task", stringField(body, "task"))
	putNonEmpty(attrs, "winlog.opcode", stringField(body, "opcode"))
	putNonEmpty(attrs, "winlog.user.identifier", nestedString(body, "security", "user_id"))
	if eventID, ok := nestedInt(body, "event_id", "id"); ok {
		attrs.PutInt("winlog.event_id", eventID)
	}
	if recordID, ok := body.Get("record_id"); ok && recordID.Type() == pcommon.ValueTypeInt {
		attrs.PutInt("winlog.record_id", recordID.Int())
	}
	if pid, ok := nestedInt(body, "execution", "process_id"); ok {
		attrs.PutInt("winlog.process.pid", pid)
	}
	if tid, ok := nestedInt(body, "execution", "thread_id"); ok {
		attrs.PutInt("winlog.process.thread.id", tid)
	}
	putKeywords(attrs, body)
}

func putKeywords(attrs, body pcommon.Map) {
	keywords, ok := body.Get(keywordsKey)
	if !ok || keywords.Type() != pcommon.ValueTypeSlice || keywords.Slice().Len() == 0 {
		return
	}
	target := attrs.PutEmptySlice("winlog.keywords")
	for i := 0; i < keywords.Slice().Len(); i++ {
		target.AppendEmpty().SetStr(keywords.Slice().At(i).AsString())
	}
}

// putEventData sets one winlog.event_data.<Name> attribute per data field and
// returns the fields for the fallback body.
func putEventData(attrs, body pcommon.Map) []dataField {
	eventData, ok := nestedMap(body, "event_data")
	if !ok {
		return nil
	}
	fields := eventDataFields(eventData)
	for _, field := range fields {
		attrs.PutStr(eventDataPrefix+field.key, field.value)
	}
	return fields
}

// moveOriginal moves the receiver's XML attribute to original_message, cut at
// the configured byte cap on a character boundary.
func (p *winlogProcessor) moveOriginal(record plog.LogRecord) {
	attrs := record.Attributes()
	value, ok := attrs.Get(logRecordOriginalKey)
	if !ok || value.Type() != pcommon.ValueTypeStr {
		return
	}
	xml := value.Str()
	attrs.Remove(logRecordOriginalKey)
	truncated := false
	if limit := p.maxOriginalBytes; limit > 0 && len(xml) > limit {
		for limit > 0 && !utf8.RuneStart(xml[limit]) {
			limit--
		}
		xml, truncated = xml[:limit], true
	}
	attrs.PutStr(originalMessageKey, xml)
	if truncated {
		attrs.PutBool(originalMessageTruncatedKey, true)
	}
}

// eventDataFields flattens EventData in either receiver format. The map
// format arrives unordered, so its keys are sorted; the array format keeps order.
func eventDataFields(eventData pcommon.Map) []dataField {
	if raw, ok := eventData.Get("data"); ok && raw.Type() == pcommon.ValueTypeSlice {
		return arrayEventData(raw.Slice())
	}
	fields := make([]dataField, 0, eventData.Len())
	eventData.Range(func(key string, value pcommon.Value) bool {
		fields = append(fields, dataField{key: key, value: value.AsString()})
		return true
	})
	sort.Slice(fields, func(i, j int) bool { return fields[i].key < fields[j].key })
	return fields
}

func arrayEventData(items pcommon.Slice) []dataField {
	var fields []dataField
	unnamed := 0
	for i := 0; i < items.Len(); i++ {
		if items.At(i).Type() != pcommon.ValueTypeMap {
			continue
		}
		items.At(i).Map().Range(func(key string, value pcommon.Value) bool {
			if key == "" {
				unnamed++
				key = "param" + strconv.Itoa(unnamed)
			}
			fields = append(fields, dataField{key: key, value: value.AsString()})
			return true
		})
	}
	return fields
}

// fallbackBody is "<provider> event <id>" followed by key=value pairs, used
// when the message cannot be rendered.
func fallbackBody(provider string, eventID int64, hasEventID bool, data []dataField) string {
	var sb strings.Builder
	sb.WriteString(provider)
	sb.WriteString(" event")
	if hasEventID {
		fmt.Fprintf(&sb, " %d", eventID)
	}
	for _, field := range data {
		sb.WriteString(" " + field.key + "=" + field.value)
	}
	return sb.String()
}

func stringField(m pcommon.Map, key string) string {
	if v, ok := m.Get(key); ok && v.Type() == pcommon.ValueTypeStr {
		return v.Str()
	}
	return ""
}

func nestedMap(m pcommon.Map, key string) (pcommon.Map, bool) {
	if v, ok := m.Get(key); ok && v.Type() == pcommon.ValueTypeMap {
		return v.Map(), true
	}
	return pcommon.Map{}, false
}

func nestedString(m pcommon.Map, key, inner string) string {
	if child, ok := nestedMap(m, key); ok {
		return stringField(child, inner)
	}
	return ""
}

func nestedInt(m pcommon.Map, key, inner string) (int64, bool) {
	child, ok := nestedMap(m, key)
	if !ok {
		return 0, false
	}
	if v, ok := child.Get(inner); ok && v.Type() == pcommon.ValueTypeInt {
		return v.Int(), true
	}
	return 0, false
}

func putNonEmpty(attrs pcommon.Map, key, value string) {
	if value != "" {
		attrs.PutStr(key, value)
	}
}
