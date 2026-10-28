// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package winlogprocessor

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

func TestDefaultConfigValidates(t *testing.T) {
	cfg := NewFactory().CreateDefaultConfig()
	if err := cfg.(*Config).Validate(); err != nil {
		t.Fatalf("default config does not validate: %v", err)
	}
}

func TestProcessorPassesData(t *testing.T) {
	sink := new(consumertest.LogsSink)
	set := processortest.NewNopSettings(component.MustNewType(typeStr))
	proc, err := NewFactory().CreateLogs(context.Background(), set, NewFactory().CreateDefaultConfig(), sink)
	if err != nil {
		t.Fatalf("create processor: %v", err)
	}
	logs := plog.NewLogs()
	logs.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty().LogRecords().AppendEmpty()
	if err := proc.ConsumeLogs(context.Background(), logs); err != nil {
		t.Fatalf("consume: %v", err)
	}
	if got := sink.LogRecordCount(); got != 1 {
		t.Fatalf("next consumer saw %d items, want 1", got)
	}
}
