// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package winlogprocessor

import (
	"strings"
	"testing"

	"go.opentelemetry.io/collector/confmap"
)

func logonRules() []DropRule {
	return []DropRule{
		{
			Name: "service-logons", Channels: []string{"Security"}, EventIDs: []string{"4624"},
			Fields: []FieldCondition{{Name: "LogonType", Equals: []string{"5"}}},
		},
		{
			Name: "machine-network-logons", Channels: []string{"Security"}, EventIDs: []string{"4624"},
			Fields: []FieldCondition{
				{Name: "LogonType", Equals: []string{"3"}},
				{Name: "TargetUserName", EndsWith: "$"},
			},
		},
	}
}

func logon(logonType, user string) testEvent {
	return testEvent{
		channel: "Security", id: 4624,
		data: map[string]any{"LogonType": logonType, "TargetUserName": user},
	}
}

func TestFieldEqualsDropsAServiceLogon(t *testing.T) {
	if name, got := droppedBy(t, logonRules(), logon("5", "SYSTEM")); !got || name != "service-logons" {
		t.Errorf("service logon dropped by (%q, %v), want service-logons", name, got)
	}
}

func TestFieldEndsWithDropsAMachineAccountNetworkLogon(t *testing.T) {
	if name, got := droppedBy(t, logonRules(), logon("3", "WS01$")); !got || name != "machine-network-logons" {
		t.Errorf("machine logon dropped by (%q, %v), want machine-network-logons", name, got)
	}
}

func TestRealLogonsPassThroughTheLogonRules(t *testing.T) {
	for _, event := range []testEvent{logon("2", "alice"), logon("3", "alice"), logon("10", "bob")} {
		if name, got := droppedBy(t, logonRules(), event); got {
			t.Errorf("logon %v was dropped by %q", event.data, name)
		}
	}
}

func TestAMachineAccountOutsideNetworkLogonsIsKept(t *testing.T) {
	if name, got := droppedBy(t, logonRules(), logon("2", "WS01$")); got {
		t.Errorf("interactive machine-account logon was dropped by %q", name)
	}
}

func TestFieldAlternativesAreORed(t *testing.T) {
	rules := []DropRule{{Name: "r", Fields: []FieldCondition{{Name: "LogonType", Equals: []string{"3", "5"}}}}}
	for _, logonType := range []string{"3", "5"} {
		if _, got := droppedBy(t, rules, logon(logonType, "x")); !got {
			t.Errorf("LogonType %s was not dropped", logonType)
		}
	}
}

func TestMissingFieldNeverMatches(t *testing.T) {
	rules := []DropRule{{Name: "r", Fields: []FieldCondition{{Name: "LogonType", Equals: []string{"5"}}}}}
	if _, got := droppedBy(t, rules, testEvent{channel: "Security", id: 4624}); got {
		t.Error("an event with no event data matched a field condition")
	}
}

func TestFieldNamesAreCaseSensitive(t *testing.T) {
	rules := []DropRule{{Name: "r", Fields: []FieldCondition{{Name: "logontype", Equals: []string{"5"}}}}}
	if _, got := droppedBy(t, rules, logon("5", "x")); got {
		t.Error("logontype matched LogonType")
	}
}

func TestIntegerFieldValueMatchesItsDecimalText(t *testing.T) {
	rules := []DropRule{{Name: "r", Fields: []FieldCondition{{Name: "LogonType", Equals: []string{"5"}}}}}
	event := testEvent{channel: "Security", id: 4624, data: map[string]any{"LogonType": int64(5)}}
	if _, got := droppedBy(t, rules, event); !got {
		t.Error("integer 5 did not match \"5\"")
	}
}

func TestFieldConditionReadsArrayFormatEventData(t *testing.T) {
	rules := []DropRule{{Name: "r", Fields: []FieldCondition{{Name: "LogonType", Equals: []string{"5"}}}}}
	event := testEvent{channel: "Security", id: 4624, dataList: []any{
		map[string]any{"TargetUserName": "SYSTEM"}, map[string]any{"LogonType": "5"},
	}}
	if _, got := droppedBy(t, rules, event); !got {
		t.Error("array-format event data was not read")
	}
}

func TestFieldConditionsStillNeedTheOtherFieldsToMatch(t *testing.T) {
	event := logon("5", "SYSTEM")
	event.id = 4625
	if name, got := droppedBy(t, logonRules(), event); got {
		t.Errorf("event 4625 was dropped by %q", name)
	}
}

func TestARuleWithOnlyFieldConditionsIsValid(t *testing.T) {
	cfg := &Config{DropRules: []DropRule{{Name: "r", Fields: []FieldCondition{{Name: "LogonType", Equals: []string{"5"}}}}}}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

func TestInvalidFieldConditionsAreRejected(t *testing.T) {
	cases := []struct {
		name    string
		field   FieldCondition
		wantErr string
	}{
		{"no name", FieldCondition{Equals: []string{"5"}}, "field name is empty"},
		{"no operator", FieldCondition{Name: "LogonType"}, "sets neither equals nor ends_with"},
		{"both operators", FieldCondition{Name: "LogonType", Equals: []string{"5"}, EndsWith: "$"}, "sets both equals and ends_with"},
		{"empty equals value", FieldCondition{Name: "LogonType", Equals: []string{""}}, "empty value"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &Config{DropRules: []DropRule{{Name: "r", Fields: []FieldCondition{tc.field}}}}
			err := cfg.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestUnknownFieldConditionKeyIsRejectedAtLoad(t *testing.T) {
	raw := map[string]any{"drop_rules": []any{map[string]any{
		"name":   "r",
		"fields": []any{map[string]any{"name": "LogonType", "contains": "5"}},
	}}}
	cfg := NewFactory().CreateDefaultConfig().(*Config)
	err := confmap.NewFromStringMap(raw).Unmarshal(cfg)
	if err == nil || !strings.Contains(err.Error(), "contains") {
		t.Fatalf("err = %v, want it to name the unknown key contains", err)
	}
}

func TestFieldConditionsLoadFromYAMLShapedConfig(t *testing.T) {
	raw := map[string]any{"drop_rules": []any{map[string]any{
		"name":   "r",
		"fields": []any{map[string]any{"name": "TargetUserName", "ends_with": "$"}},
	}}}
	cfg := NewFactory().CreateDefaultConfig().(*Config)
	if err := confmap.NewFromStringMap(raw).Unmarshal(cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got := cfg.DropRules[0].Fields[0].EndsWith; got != "$" {
		t.Errorf("ends_with = %q, want $", got)
	}
}
