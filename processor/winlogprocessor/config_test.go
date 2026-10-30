// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package winlogprocessor

import (
	"strings"
	"testing"
)

func TestConfigAcceptsValidRules(t *testing.T) {
	cases := map[string][]DropRule{
		"no rules": nil,
		"all fields": {{
			Name: "a", Channels: []string{"System"}, EventIDs: []string{"7036", "10000-10010"},
			Levels: []int{4}, Providers: []string{"SCM"},
		}},
		"levels at the bounds": {{Name: "a", Levels: []int{0, 5}}},
	}
	for name, rules := range cases {
		t.Run(name, func(t *testing.T) {
			if err := (&Config{DropRules: rules}).Validate(); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestConfigRejectsInvalidRules(t *testing.T) {
	cases := []struct {
		name    string
		rules   []DropRule
		wantErr string
	}{
		{"empty name", []DropRule{{Channels: []string{"System"}}}, "name is empty"},
		{"duplicate name", []DropRule{{Name: "a", Channels: []string{"System"}}, {Name: "a", Channels: []string{"Security"}}}, `duplicate rule name "a"`},
		{"no fields", []DropRule{{Name: "a"}}, "sets no field"},
		{"reversed range", []DropRule{{Name: "a", EventIDs: []string{"20-10"}}}, "lo 20 is greater than hi 10"},
		{"bad event id", []DropRule{{Name: "a", EventIDs: []string{"abc"}}}, `invalid event id "abc"`},
		{"bad range bound", []DropRule{{Name: "a", EventIDs: []string{"1-x"}}}, `invalid event id "1-x"`},
		{"level too high", []DropRule{{Name: "a", Levels: []int{6}}}, "level 6 is outside 0-5"},
		{"level negative", []DropRule{{Name: "a", Levels: []int{-1}}}, "level -1 is outside 0-5"},
		{"empty channel", []DropRule{{Name: "a", Channels: []string{""}}}, "channel is empty"},
		{"empty provider", []DropRule{{Name: "a", Providers: []string{""}}}, "provider is empty"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := (&Config{DropRules: tc.rules}).Validate()
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestDefaultConfigCarriesDefaultDropRules(t *testing.T) {
	cfg := NewFactory().CreateDefaultConfig().(*Config)
	names := make([]string, 0, len(cfg.DropRules))
	for _, r := range cfg.DropRules {
		names = append(names, r.Name)
	}
	if got, want := strings.Join(names, ","), "security-audit-noise,collector-self-info"; got != want {
		t.Fatalf("default rule names = %q, want %q", got, want)
	}
}
