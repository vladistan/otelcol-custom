// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package winlogprocessor

import (
	"context"
	"strings"
	"testing"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
)

// receiverEvent mirrors what the windowseventlog receiver emits with the
// settings the sample config uses (raw: false, include_log_record_original: true).
type receiverEvent struct {
	channel             string
	provider            string
	guid                string
	computer            string
	id                  int64
	recordID            int64
	level               string
	task                string
	opcode              string
	keywords            []any
	message             string
	userID              string
	processID, threadID int64
	eventData           map[string]any
	xml                 string
}

func (e receiverEvent) toLogRecord() plog.LogRecord {
	body := map[string]any{
		"channel":    e.channel,
		"computer":   e.computer,
		"event_id":   map[string]any{"id": e.id, "qualifiers": int64(0)},
		"provider":   map[string]any{"name": e.provider, "guid": e.guid},
		"record_id":  e.recordID,
		"level":      e.level,
		"message":    e.message,
		"task":       e.task,
		"opcode":     e.opcode,
		"keywords":   e.keywords,
		"event_data": e.eventData,
	}
	if e.userID != "" {
		body["security"] = map[string]any{"user_id": e.userID}
	}
	if e.processID != 0 {
		body["execution"] = map[string]any{"process_id": e.processID, "thread_id": e.threadID}
	}
	record := plog.NewLogRecord()
	if err := record.Body().SetEmptyMap().FromRaw(body); err != nil {
		panic(err)
	}
	if e.xml != "" {
		record.Attributes().PutStr("log.record.original", e.xml)
	}
	return record
}

func xmlWith(level, keywords string) string {
	return "<Event><System><Level>" + level + "</Level><Keywords>" + keywords + "</Keywords></System></Event>"
}

func shapeEvent(t *testing.T, maxBytes int, event receiverEvent) plog.LogRecord {
	t.Helper()
	h := newHarnessCfg(t, &Config{OriginalMessageMaxBytes: maxBytes})
	logs := plog.NewLogs()
	event.toLogRecord().CopyTo(logs.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty().LogRecords().AppendEmpty())
	if err := h.proc.ConsumeLogs(context.Background(), logs); err != nil {
		t.Fatalf("consume: %v", err)
	}
	if got := h.sink.LogRecordCount(); got != 1 {
		t.Fatalf("forwarded %d records, want 1", got)
	}
	return h.sink.AllLogs()[0].ResourceLogs().At(0).ScopeLogs().At(0).LogRecords().At(0)
}

func attrString(t *testing.T, record plog.LogRecord, key string) string {
	t.Helper()
	v, ok := record.Attributes().Get(key)
	if !ok {
		t.Fatalf("attribute %q is missing", key)
	}
	return v.AsString()
}

func attrInt(t *testing.T, record plog.LogRecord, key string) int64 {
	t.Helper()
	v, ok := record.Attributes().Get(key)
	if !ok || v.Type() != pcommon.ValueTypeInt {
		t.Fatalf("attribute %q = %v, want an int", key, v)
	}
	return v.Int()
}

func TestSeverityFollowsNumericLevel(t *testing.T) {
	cases := []struct {
		level  string
		number plog.SeverityNumber
		text   string
	}{
		{"1", plog.SeverityNumberFatal, "FATAL"},
		{"2", plog.SeverityNumberError, "ERROR"},
		{"3", plog.SeverityNumberWarn, "WARN"},
		{"4", plog.SeverityNumberInfo, "INFO"},
		{"5", plog.SeverityNumberDebug, "DEBUG"},
		{"0", plog.SeverityNumberInfo, "INFO"},
	}
	for _, tc := range cases {
		t.Run("level "+tc.level, func(t *testing.T) {
			record := shapeEvent(t, 0, receiverEvent{channel: "Application", xml: xmlWith(tc.level, "0x80000000000000")})
			if record.SeverityNumber() != tc.number || record.SeverityText() != tc.text {
				t.Errorf("severity = (%d, %q), want (%d, %q)", record.SeverityNumber(), record.SeverityText(), tc.number, tc.text)
			}
		})
	}
}

func TestAuditFailureKeywordMapsToWarn(t *testing.T) {
	record := shapeEvent(t, 0, receiverEvent{channel: "Security", id: 4625, xml: xmlWith("0", "0x8010000000000000")})
	if record.SeverityNumber() != plog.SeverityNumberWarn || record.SeverityText() != "WARN" {
		t.Errorf("severity = (%d, %q), want WARN", record.SeverityNumber(), record.SeverityText())
	}
}

func TestAuditSuccessKeywordKeepsLevelSeverity(t *testing.T) {
	record := shapeEvent(t, 0, receiverEvent{channel: "Security", id: 4624, xml: xmlWith("0", "0x8020000000000000")})
	if record.SeverityNumber() != plog.SeverityNumberInfo {
		t.Errorf("severity number = %d, want INFO", record.SeverityNumber())
	}
}

func TestSeverityFallsBackToRenderedLevelWithoutXML(t *testing.T) {
	record := shapeEvent(t, 0, receiverEvent{channel: "System", level: "Warning"})
	if record.SeverityNumber() != plog.SeverityNumberWarn || record.SeverityText() != "WARN" {
		t.Errorf("severity = (%d, %q), want WARN", record.SeverityNumber(), record.SeverityText())
	}
}

func TestSeverityStaysUnsetWhenLevelIsUnknown(t *testing.T) {
	record := shapeEvent(t, 0, receiverEvent{channel: "System"})
	if record.SeverityNumber() != plog.SeverityNumberUnspecified {
		t.Errorf("severity number = %d, want unspecified", record.SeverityNumber())
	}
}

func TestSeverityIsReadBeforeTheXMLIsCut(t *testing.T) {
	padding := strings.Repeat("x", 200)
	xml := "<Event><Pad>" + padding + "</Pad>" + xmlWith("2", "0x80000000000000") + "</Event>"
	record := shapeEvent(t, 50, receiverEvent{channel: "System", xml: xml})
	if record.SeverityNumber() != plog.SeverityNumberError {
		t.Errorf("severity number = %d, want ERROR from a level past the cut", record.SeverityNumber())
	}
}

var fullSecurityEvent = receiverEvent{
	channel: "Security", provider: "Microsoft-Windows-Security-Auditing", guid: "{54849625}",
	computer: "HOST1", id: 4625, recordID: 99, task: "Logon", opcode: "Info",
	keywords: []any{"Audit Failure"}, userID: "S-1-5-18", processID: 612, threadID: 700,
	xml: xmlWith("0", "0x8010000000000000"),
}

func TestWinlogStringAttributesCarryTheEventFields(t *testing.T) {
	record := shapeEvent(t, 0, fullSecurityEvent)
	for key, want := range map[string]string{
		"winlog.channel":         "Security",
		"winlog.provider_name":   "Microsoft-Windows-Security-Auditing",
		"winlog.provider_guid":   "{54849625}",
		"winlog.computer_name":   "HOST1",
		"winlog.task":            "Logon",
		"winlog.opcode":          "Info",
		"winlog.user.identifier": "S-1-5-18",
		"log.source":             "winlog:Security",
	} {
		if got := attrString(t, record, key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
}

func TestWinlogIntegerAttributesCarryTheEventFields(t *testing.T) {
	record := shapeEvent(t, 0, fullSecurityEvent)
	for key, want := range map[string]int64{
		"winlog.event_id":          4625,
		"winlog.record_id":         99,
		"winlog.process.pid":       612,
		"winlog.process.thread.id": 700,
	} {
		if got := attrInt(t, record, key); got != want {
			t.Errorf("%s = %d, want %d", key, got, want)
		}
	}
}

func TestWinlogKeywordsAttributeIsAStringArray(t *testing.T) {
	record := shapeEvent(t, 0, fullSecurityEvent)
	keywords, ok := record.Attributes().Get("winlog.keywords")
	if !ok || keywords.Type() != pcommon.ValueTypeSlice || keywords.Slice().Len() != 1 || keywords.Slice().At(0).Str() != "Audit Failure" {
		t.Errorf("winlog.keywords = %v, want [Audit Failure]", keywords)
	}
}

func TestOptionalWinlogAttributesAreAbsentWhenTheEventLacksThem(t *testing.T) {
	record := shapeEvent(t, 0, receiverEvent{channel: "System", provider: "p", id: 1})
	for _, key := range []string{"winlog.user.identifier", "winlog.process.pid", "winlog.process.thread.id"} {
		if _, ok := record.Attributes().Get(key); ok {
			t.Errorf("attribute %q is set, want it absent", key)
		}
	}
}

func TestEventDataKeysBecomeNamedAndNumberedAttributes(t *testing.T) {
	record := shapeEvent(t, 0, receiverEvent{
		channel: "Security", provider: "p", id: 1,
		eventData: map[string]any{"TargetUserName": "alice", "param1": "raw"},
	})
	if got := attrString(t, record, "winlog.event_data.TargetUserName"); got != "alice" {
		t.Errorf("TargetUserName = %q, want alice", got)
	}
	if got := attrString(t, record, "winlog.event_data.param1"); got != "raw" {
		t.Errorf("param1 = %q, want raw", got)
	}
}

func TestArrayFormatEventDataNumbersUnnamedElements(t *testing.T) {
	record := shapeEvent(t, 0, receiverEvent{
		channel: "Security", provider: "p", id: 1,
		eventData: map[string]any{"data": []any{
			map[string]any{"Name1": "a"}, map[string]any{"": "b"}, map[string]any{"": "c"},
		}},
	})
	for key, want := range map[string]string{
		"winlog.event_data.Name1": "a", "winlog.event_data.param1": "b", "winlog.event_data.param2": "c",
	} {
		if got := attrString(t, record, key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
}

func TestBodyIsTheRenderedMessage(t *testing.T) {
	record := shapeEvent(t, 0, receiverEvent{channel: "System", provider: "p", id: 7036, message: "The service entered the running state."})
	if record.Body().Type() != pcommon.ValueTypeStr || record.Body().Str() != "The service entered the running state." {
		t.Errorf("body = %v, want the message text", record.Body().AsRaw())
	}
}

func TestBodyFallsBackToProviderEventAndData(t *testing.T) {
	record := shapeEvent(t, 0, receiverEvent{
		channel: "Application", provider: "OldProvider", id: 1000,
		eventData: map[string]any{"b": "2", "a": "1"},
	})
	if got, want := record.Body().Str(), "OldProvider event 1000 a=1 b=2"; got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
}

func TestBodyFallbackWithoutEventData(t *testing.T) {
	record := shapeEvent(t, 0, receiverEvent{channel: "Application", provider: "OldProvider", id: 1000})
	if got, want := record.Body().Str(), "OldProvider event 1000"; got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
}

func TestOriginalMessageReplacesTheReceiverAttribute(t *testing.T) {
	xml := xmlWith("4", "0x80000000000000")
	record := shapeEvent(t, 0, receiverEvent{channel: "System", xml: xml})
	if got := attrString(t, record, "original_message"); got != xml {
		t.Errorf("original_message = %q, want the event XML", got)
	}
	if _, ok := record.Attributes().Get("log.record.original"); ok {
		t.Error("log.record.original is still set")
	}
	if _, ok := record.Attributes().Get("original_message.truncated"); ok {
		t.Error("original_message.truncated is set on an uncut message")
	}
}

func TestOriginalMessageIsCutAtTheCapAndFlagged(t *testing.T) {
	xml := "<Event>" + strings.Repeat("x", 100) + "</Event>"
	record := shapeEvent(t, 64, receiverEvent{channel: "System", xml: xml})
	if got := attrString(t, record, "original_message"); got != xml[:64] {
		t.Errorf("original_message has %d bytes, want the first 64", len(got))
	}
	v, ok := record.Attributes().Get("original_message.truncated")
	if !ok || !v.Bool() {
		t.Errorf("original_message.truncated = %v, want true", v)
	}
}

func TestOriginalMessageCutKeepsMultiByteCharactersWhole(t *testing.T) {
	xml := strings.Repeat("é", 40) // 80 bytes, 2 per rune
	record := shapeEvent(t, 51, receiverEvent{channel: "System", xml: xml})
	got := attrString(t, record, "original_message")
	if len(got) != 50 || !strings.HasSuffix(got, "é") {
		t.Errorf("cut message has %d bytes, want 50 ending in a whole é", len(got))
	}
}

func TestZeroCapKeepsTheWholeXML(t *testing.T) {
	xml := "<Event>" + strings.Repeat("x", 100000) + "</Event>"
	record := shapeEvent(t, 0, receiverEvent{channel: "System", xml: xml})
	if got := attrString(t, record, "original_message"); got != xml {
		t.Errorf("original_message has %d bytes, want %d", len(got), len(xml))
	}
}

func TestConfigRejectsNegativeOriginalMessageCap(t *testing.T) {
	err := (&Config{OriginalMessageMaxBytes: -1}).Validate()
	if err == nil || !strings.Contains(err.Error(), "original_message_max_bytes") {
		t.Fatalf("err = %v, want it to name original_message_max_bytes", err)
	}
}

func TestDefaultConfigCapsTheOriginalMessageAt64KiB(t *testing.T) {
	cfg := NewFactory().CreateDefaultConfig().(*Config)
	if cfg.OriginalMessageMaxBytes != 64*1024 {
		t.Errorf("default cap = %d, want 65536", cfg.OriginalMessageMaxBytes)
	}
}
