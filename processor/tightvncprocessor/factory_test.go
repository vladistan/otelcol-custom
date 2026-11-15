// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package tightvncprocessor

import (
	"context"
	"testing"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/processor/processortest"
)

func TestFactoryType(t *testing.T) {
	if got, want := NewFactory().Type(), component.MustNewType(typeStr); got != want {
		t.Fatalf("factory type = %v, want %v", got, want)
	}
}

func TestCreateLogs_InvalidConfigReturnsValidationError(t *testing.T) {
	cfg := defaultConfig()
	cfg.DropPrefixes = []string{""}
	set := processortest.NewNopSettings(component.MustNewType(typeStr))

	proc, err := NewFactory().CreateLogs(context.Background(), set, cfg, new(consumertest.LogsSink))

	if want := cfg.Validate(); err == nil || err.Error() != want.Error() {
		t.Errorf("CreateLogs error = %v, want the Validate error %v", err, want)
	}
	if proc != nil {
		t.Error("CreateLogs returned a processor for an invalid config")
	}
}
