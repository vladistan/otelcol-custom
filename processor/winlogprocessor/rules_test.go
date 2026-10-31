// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package winlogprocessor

import (
	"testing"

	"go.opentelemetry.io/collector/pdata/plog"
)

type testEvent struct {
	channel  string
	id       int64
	provider string
	xmlLevel string
	rendered string
	message  string
	// data is event_data in the map format; dataList is the array format.
	data     map[string]any
	dataList []any
}

func (e testEvent) toLogRecord() plog.LogRecord {
	record := plog.NewLogRecord()
	body := record.Body().SetEmptyMap()
	body.PutStr("channel", e.channel)
	body.PutEmptyMap("event_id").PutInt("id", e.id)
	body.PutEmptyMap("provider").PutStr("name", e.provider)
	if e.rendered != "" {
		body.PutStr("level", e.rendered)
	}
	if e.message != "" {
		body.PutStr("message", e.message)
	}
	if e.data != nil {
		if err := body.PutEmptyMap("event_data").FromRaw(e.data); err != nil {
			panic(err)
		}
	}
	if e.dataList != nil {
		if err := body.PutEmptyMap("event_data").PutEmptySlice("data").FromRaw(e.dataList); err != nil {
			panic(err)
		}
	}
	if e.xmlLevel != "" {
		record.Attributes().PutStr("log.record.original",
			"<Event><System><Level>"+e.xmlLevel+"</Level></System></Event>")
	}
	return record
}

// droppedBy runs one event through the processor and returns the name of the
// rule that dropped it.
func droppedBy(t *testing.T, rules []DropRule, event testEvent) (string, bool) {
	t.Helper()
	h := newHarness(t, rules)
	if err := h.proc.ConsumeLogs(t.Context(), batch(event)); err != nil {
		t.Fatalf("consume: %v", err)
	}
	return h.droppedRule(t)
}

func TestRuleFieldsAreANDed(t *testing.T) {
	rules := []DropRule{{Name: "r", Channels: []string{"Security"}, EventIDs: []string{"4624"}}}
	cases := []struct {
		name      string
		event     testEvent
		isDropped bool
	}{
		{"both match", testEvent{channel: "Security", id: 4624}, true},
		{"channel differs", testEvent{channel: "System", id: 4624}, false},
		{"id differs", testEvent{channel: "Security", id: 4625}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, got := droppedBy(t, rules, tc.event); got != tc.isDropped {
				t.Errorf("dropped = %v, want %v", got, tc.isDropped)
			}
		})
	}
}

func TestRuleValuesInAFieldAreORed(t *testing.T) {
	rules := []DropRule{{Name: "r", Channels: []string{"Security", "System"}, EventIDs: []string{"1", "5"}}}
	for _, event := range []testEvent{
		{channel: "Security", id: 1}, {channel: "System", id: 5}, {channel: "Security", id: 5},
	} {
		if _, got := droppedBy(t, rules, event); !got {
			t.Errorf("%+v was not dropped", event)
		}
	}
	if _, got := droppedBy(t, rules, testEvent{channel: "Application", id: 1}); got {
		t.Error("Application was dropped by a Security/System rule")
	}
}

func TestRulesAreORed(t *testing.T) {
	rules := []DropRule{
		{Name: "first", EventIDs: []string{"100"}},
		{Name: "second", EventIDs: []string{"200"}},
	}
	for _, id := range []int64{100, 200} {
		if _, got := droppedBy(t, rules, testEvent{channel: "c", id: id}); !got {
			t.Errorf("event %d was not dropped", id)
		}
	}
}

func TestFirstMatchingRuleNamesTheDrop(t *testing.T) {
	rules := []DropRule{
		{Name: "first", EventIDs: []string{"100"}},
		{Name: "second", EventIDs: []string{"100", "200"}},
	}
	if name, _ := droppedBy(t, rules, testEvent{channel: "c", id: 100}); name != "first" {
		t.Errorf("event 100 dropped by %q, want first", name)
	}
	if name, _ := droppedBy(t, rules, testEvent{channel: "c", id: 200}); name != "second" {
		t.Errorf("event 200 dropped by %q, want second", name)
	}
}

func TestEventIDRangesAreInclusive(t *testing.T) {
	rules := []DropRule{{Name: "r", EventIDs: []string{"10000-10010"}}}
	for id, want := range map[int64]bool{9999: false, 10000: true, 10005: true, 10010: true, 10011: false} {
		if _, got := droppedBy(t, rules, testEvent{channel: "System", id: id}); got != want {
			t.Errorf("id %d dropped = %v, want %v", id, got, want)
		}
	}
}

func TestNumericLevelFromXML(t *testing.T) {
	rules := []DropRule{{Name: "r", Levels: []int{4}}}
	cases := []struct {
		xmlLevel  string
		isDropped bool
	}{{"4", true}, {"2", false}, {"0", false}}
	for _, tc := range cases {
		event := testEvent{channel: "Application", id: 1, xmlLevel: tc.xmlLevel}
		if _, got := droppedBy(t, rules, event); got != tc.isDropped {
			t.Errorf("level %s dropped = %v, want %v", tc.xmlLevel, got, tc.isDropped)
		}
	}
}

func TestRenderedLevelNameIsUsedWhenXMLIsMissing(t *testing.T) {
	rules := []DropRule{{Name: "r", Levels: []int{4}}}
	if _, got := droppedBy(t, rules, testEvent{channel: "c", id: 1, rendered: "Information"}); !got {
		t.Error("rendered Information was not dropped by a level 4 rule")
	}
}

func TestRenderedLevelOfAnotherSeverityIsKept(t *testing.T) {
	rules := []DropRule{{Name: "r", Levels: []int{4}}}
	if _, got := droppedBy(t, rules, testEvent{channel: "c", id: 1, rendered: "Warning"}); got {
		t.Error("rendered Warning was dropped by a level 4 rule")
	}
}

func TestUnknownLevelIsNeverDroppedByALevelRule(t *testing.T) {
	rules := []DropRule{{Name: "r", Levels: []int{4}}}
	if _, got := droppedBy(t, rules, testEvent{channel: "c", id: 1}); got {
		t.Error("an event with no level was dropped by a level rule")
	}
}

func TestProvidersAndChannelsMatchCaseInsensitively(t *testing.T) {
	rules := []DropRule{{Name: "r", Channels: []string{"security"}, Providers: []string{"Microsoft-Windows-Security-Auditing"}}}
	event := testEvent{channel: "Security", id: 4624, provider: "microsoft-windows-security-auditing"}
	if _, got := droppedBy(t, rules, event); !got {
		t.Error("case-insensitive match failed")
	}
}

const archiverChannel = "Microsoft-Windows-SystemDataArchiver/Diagnostic"

func TestDefaultListDropsArchiverInformationAndVerbose(t *testing.T) {
	for _, level := range []string{"4", "5"} {
		for _, id := range []int64{2049, 2050} {
			event := testEvent{channel: archiverChannel, id: id, xmlLevel: level}
			if name, got := droppedBy(t, DefaultDropRules(), event); !got || name != "system-data-archiver-noise" {
				t.Errorf("id %d level %s dropped by (%q, %v), want system-data-archiver-noise", id, level, name, got)
			}
		}
	}
}

func TestDefaultListKeepsArchiverWarning(t *testing.T) {
	event := testEvent{channel: archiverChannel, id: 2049, xmlLevel: "3"}
	if name, got := droppedBy(t, DefaultDropRules(), event); got {
		t.Errorf("archiver Warning was dropped by %q", name)
	}
}

func TestDefaultListKeepsOtherArchiverEvents(t *testing.T) {
	event := testEvent{channel: archiverChannel, id: 2051, xmlLevel: "4"}
	if name, got := droppedBy(t, DefaultDropRules(), event); got {
		t.Errorf("archiver event 2051 was dropped by %q", name)
	}
}

func TestLevelFiveMatchesFromXML(t *testing.T) {
	rules := []DropRule{{Name: "r", Levels: []int{5}}}
	if _, got := droppedBy(t, rules, testEvent{channel: "c", id: 1, xmlLevel: "5"}); !got {
		t.Error("XML level 5 was not matched by a level 5 rule")
	}
}

func TestLevelFiveMatchesRenderedVerbose(t *testing.T) {
	rules := []DropRule{{Name: "r", Levels: []int{5}}}
	if _, got := droppedBy(t, rules, testEvent{channel: "c", id: 1, rendered: "Verbose"}); !got {
		t.Error("rendered Verbose was not matched by a level 5 rule")
	}
}

func TestLevelFiveMatchesRawDigitWithoutXML(t *testing.T) {
	rules := []DropRule{{Name: "r", Levels: []int{5}}}
	if _, got := droppedBy(t, rules, testEvent{channel: "c", id: 1, rendered: "5"}); !got {
		t.Error("raw level \"5\" was not matched by a level 5 rule")
	}
}

func TestRawDigitLevelOutsideRangeIsUnknown(t *testing.T) {
	rules := []DropRule{{Name: "r", Levels: []int{4, 5}}}
	if _, got := droppedBy(t, rules, testEvent{channel: "c", id: 1, rendered: "9"}); got {
		t.Error("raw level \"9\" was matched by a level rule")
	}
}

func TestDefaultListDropsNoiseAndKeepsFailedLogon(t *testing.T) {
	cases := []struct {
		name     string
		event    testEvent
		wantRule string
	}{
		{"4634 logoff", testEvent{channel: "Security", id: 4634}, "security-audit-noise"},
		{"4672 privileges", testEvent{channel: "Security", id: 4672}, "security-audit-noise"},
		{"5379 credential read", testEvent{channel: "Security", id: 5379}, "security-audit-noise"},
		{"4624 logon kept", testEvent{channel: "Security", id: 4624}, ""},
		{"4658 handle closed kept", testEvent{channel: "Security", id: 4658}, ""},
		{"5156 WFP permit kept", testEvent{channel: "Security", id: 5156}, ""},
		{"5158 WFP bind kept", testEvent{channel: "Security", id: 5158}, ""},
		{"4625 failed logon", testEvent{channel: "Security", id: 4625}, ""},
		{"5157 WFP block", testEvent{channel: "Security", id: 5157}, ""},
		{"4624 outside Security", testEvent{channel: "Application", id: 4624}, ""},
		{"self info", testEvent{channel: "Application", id: 1, provider: "otelcol-edge", xmlLevel: "4"}, "collector-self-info"},
		{"self error kept", testEvent{channel: "Application", id: 1, provider: "otelcol-edge", xmlLevel: "2"}, ""},
		{"other provider info kept", testEvent{channel: "Application", id: 1, provider: "other", xmlLevel: "4"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			name, isDropped := droppedBy(t, DefaultDropRules(), tc.event)
			if isDropped != (tc.wantRule != "") || name != tc.wantRule {
				t.Errorf("dropped by (%q, %v), want rule %q", name, isDropped, tc.wantRule)
			}
		})
	}
}
