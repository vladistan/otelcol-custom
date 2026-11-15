// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package containernameprocessor

import (
	"context"
	"errors"
	"testing"

	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/pipeline"
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

func TestNewFactory_OnlyLogsAreSupported(t *testing.T) {
	f := NewFactory()
	set := processortest.NewNopSettings(f.Type())

	if _, err := f.CreateMetrics(context.Background(), set, f.CreateDefaultConfig(), consumertest.NewNop()); !errors.Is(err, pipeline.ErrSignalNotSupported) {
		t.Errorf("metrics error = %v, want ErrSignalNotSupported", err)
	}
	if _, err := f.CreateTraces(context.Background(), set, f.CreateDefaultConfig(), consumertest.NewNop()); !errors.Is(err, pipeline.ErrSignalNotSupported) {
		t.Errorf("traces error = %v, want ErrSignalNotSupported", err)
	}
}
