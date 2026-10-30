// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package winlogprocessor

import (
	"os"
	"slices"
	"testing"

	"go.opentelemetry.io/collector/confmap"
	"gopkg.in/yaml.v3"
)

const windowsEdgeSample = "../../test/configs/windows-edge.yaml"

func loadSampleWinlogConfig(t *testing.T) *Config {
	t.Helper()
	raw, err := os.ReadFile(windowsEdgeSample)
	if err != nil {
		t.Fatalf("read sample config: %v", err)
	}
	var tree map[string]any
	if err := yaml.Unmarshal(raw, &tree); err != nil {
		t.Fatalf("parse sample config: %v", err)
	}
	section, err := confmap.NewFromStringMap(tree).Sub("processors::winlog")
	if err != nil {
		t.Fatalf("processors::winlog: %v", err)
	}
	cfg := NewFactory().CreateDefaultConfig().(*Config)
	if err := section.Unmarshal(cfg); err != nil {
		t.Fatalf("sample winlog block does not unmarshal into Config: %v", err)
	}
	return cfg
}

func TestSampleWinlogBlockValidates(t *testing.T) {
	if err := loadSampleWinlogConfig(t).Validate(); err != nil {
		t.Fatalf("sample winlog block is invalid: %v", err)
	}
}

func TestSampleWinlogBlockUsesTheDefaultOriginalMessageCap(t *testing.T) {
	if got := loadSampleWinlogConfig(t).OriginalMessageMaxBytes; got != defaultOriginalMessageMaxBytes {
		t.Errorf("original_message_max_bytes = %d, want %d", got, defaultOriginalMessageMaxBytes)
	}
}

func TestSampleDropListMatchesTheBuiltInDefaults(t *testing.T) {
	cfg := loadSampleWinlogConfig(t)
	want := DefaultDropRules()
	if len(cfg.DropRules) != len(want) {
		t.Fatalf("sample config has %d rules, built-in default has %d", len(cfg.DropRules), len(want))
	}
	for i, rule := range cfg.DropRules {
		if rule.Name != want[i].Name || !slices.Equal(rule.EventIDs, want[i].EventIDs) ||
			!slices.Equal(rule.Levels, want[i].Levels) || !slices.Equal(rule.Providers, want[i].Providers) ||
			!slices.Equal(rule.Channels, want[i].Channels) {
			t.Errorf("sample rule %d = %+v, want %+v", i, rule, want[i])
		}
	}
}
