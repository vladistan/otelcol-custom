// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package containernameprocessor

import (
	"context"
	"testing"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/processor/processortest"
)

func TestNewFactory_TypeIsContainername(t *testing.T) {
	if got := NewFactory().Type().String(); got != "containername" {
		t.Errorf("type = %q, want %q", got, "containername")
	}
}

func TestNewFactory_DefaultConfigHasNoKeys(t *testing.T) {
	cfg, ok := NewFactory().CreateDefaultConfig().(*Config)
	if !ok {
		t.Fatalf("default config is %T, want *Config", NewFactory().CreateDefaultConfig())
	}

	if err := cfg.Validate(); err != nil {
		t.Errorf("validate: %v", err)
	}
}

func TestNewFactory_LogsMetricsAndTracesAreSupported(t *testing.T) {
	f := NewFactory()
	set := processortest.NewNopSettings(f.Type())

	if _, err := f.CreateLogs(context.Background(), set, f.CreateDefaultConfig(), consumertest.NewNop()); err != nil {
		t.Errorf("logs error = %v", err)
	}
	if _, err := f.CreateMetrics(context.Background(), set, f.CreateDefaultConfig(), consumertest.NewNop()); err != nil {
		t.Errorf("metrics error = %v", err)
	}
	if _, err := f.CreateTraces(context.Background(), set, f.CreateDefaultConfig(), consumertest.NewNop()); err != nil {
		t.Errorf("traces error = %v", err)
	}
}

func TestNewFactory_StabilityIsDevelopment(t *testing.T) {
	if got := NewFactory().LogsStability(); got != component.StabilityLevelDevelopment {
		t.Errorf("logs stability = %v, want development", got)
	}
	if got := NewFactory().MetricsStability(); got != component.StabilityLevelDevelopment {
		t.Errorf("metrics stability = %v, want development", got)
	}
	if got := NewFactory().TracesStability(); got != component.StabilityLevelDevelopment {
		t.Errorf("traces stability = %v, want development", got)
	}
}
