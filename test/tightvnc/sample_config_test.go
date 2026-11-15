// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package tightvnc

import (
	"os"
	"slices"
	"testing"

	"go.opentelemetry.io/collector/confmap"
	"gopkg.in/yaml.v3"

	"github.com/vladistan/otelcol-custom/processor/tightvncprocessor"
)

const windowsEdgeSample = "../configs/windows-edge.yaml"

func loadSample(t *testing.T) *confmap.Conf {
	t.Helper()
	raw, err := os.ReadFile(windowsEdgeSample)
	if err != nil {
		t.Fatalf("read sample config: %v", err)
	}
	var tree map[string]any
	if err := yaml.Unmarshal(raw, &tree); err != nil {
		t.Fatalf("parse sample config: %v", err)
	}
	return confmap.NewFromStringMap(tree)
}

func TestSample_ReadsTheLogThroughFilelogWithNoOperators(t *testing.T) {
	block, err := loadSample(t).Sub("receivers::filelog/tightvnc")
	if err != nil {
		t.Fatalf("receivers::filelog/tightvnc: %v", err)
	}

	got := block.ToStringMap()

	want := map[string]any{"encoding": "utf-16le", "start_at": "end", "storage": "file_storage"}
	for key, value := range want {
		if got[key] != value {
			t.Errorf("filelog/tightvnc %s = %v, want %v", key, got[key], value)
		}
	}
	if _, ok := got["operators"]; ok {
		t.Error("filelog/tightvnc sets operators, want none: the processor does the parsing")
	}
	if include, _ := got["include"].([]any); len(include) != 1 {
		t.Errorf("filelog/tightvnc include = %v, want one path", got["include"])
	}
}

func TestSample_TightvncBlockUnmarshalsAndValidates(t *testing.T) {
	block, err := loadSample(t).Sub("processors::tightvnc")
	if err != nil {
		t.Fatalf("processors::tightvnc: %v", err)
	}
	cfg := tightvncprocessor.NewFactory().CreateDefaultConfig().(*tightvncprocessor.Config)

	if err := block.Unmarshal(cfg); err != nil {
		t.Fatalf("sample tightvnc block does not unmarshal into Config: %v", err)
	}

	if err := cfg.Validate(); err != nil {
		t.Errorf("sample tightvnc block is invalid: %v", err)
	}
}

func TestSample_PipelineRunsTightvncRightAfterTheMemoryLimiter(t *testing.T) {
	block, err := loadSample(t).Sub("service::pipelines::logs/tightvnc")
	if err != nil {
		t.Fatalf("service::pipelines::logs/tightvnc: %v", err)
	}
	got := block.ToStringMap()

	var processors []string
	for _, p := range got["processors"].([]any) {
		processors = append(processors, p.(string))
	}
	if i, j := slices.Index(processors, "memory_limiter"), slices.Index(processors, "tightvnc"); i < 0 || j != i+1 {
		t.Errorf("processors = %v, want tightvnc directly after memory_limiter", processors)
	}
	if receivers, _ := got["receivers"].([]any); len(receivers) != 1 || receivers[0] != "filelog/tightvnc" {
		t.Errorf("receivers = %v, want [filelog/tightvnc]", got["receivers"])
	}
}
