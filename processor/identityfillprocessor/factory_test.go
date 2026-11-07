// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package identityfillprocessor

import (
	"context"
	"testing"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/processor/processortest"
)

func TestFactoryType(t *testing.T) {
	if got, want := NewFactory().Type(), component.MustNewType(typeStr); got != want {
		t.Fatalf("factory type = %v, want %v", got, want)
	}
}

func TestCreateLogs_InvalidConfigReturnsValidationError(t *testing.T) {
	cfg := defaultConfig()
	cfg.HostName.Sources = nil
	set := processortest.NewNopSettings(component.MustNewType(typeStr))

	proc, err := NewFactory().CreateLogs(context.Background(), set, cfg, new(consumertest.LogsSink))

	if want := cfg.Validate(); err == nil || err.Error() != want.Error() {
		t.Errorf("CreateLogs error = %v, want the Validate error %v", err, want)
	}
	if proc != nil {
		t.Error("CreateLogs returned a processor for an invalid config")
	}
}

func TestProcessorPassesData(t *testing.T) {
	sink := new(consumertest.LogsSink)
	set := processortest.NewNopSettings(component.MustNewType(typeStr))
	proc, err := NewFactory().CreateLogs(context.Background(), set, defaultConfig(), sink)
	if err != nil {
		t.Fatalf("create processor: %v", err)
	}
	logs := plog.NewLogs()
	logs.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty().LogRecords().AppendEmpty()

	if err := proc.ConsumeLogs(context.Background(), logs); err != nil {
		t.Fatalf("consume: %v", err)
	}

	if got := sink.LogRecordCount(); got != 1 {
		t.Fatalf("next consumer saw %d records, want 1", got)
	}
}
