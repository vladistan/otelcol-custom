// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package windowhistprocessor

import (
	"context"
	"testing"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/pdata/pmetric"
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
	sink := new(consumertest.MetricsSink)
	set := processortest.NewNopSettings(component.MustNewType(typeStr))
	proc, err := NewFactory().CreateMetrics(context.Background(), set, NewFactory().CreateDefaultConfig(), sink)
	if err != nil {
		t.Fatalf("create processor: %v", err)
	}
	metrics := pmetric.NewMetrics()
	metrics.ResourceMetrics().AppendEmpty().ScopeMetrics().AppendEmpty().Metrics().AppendEmpty().SetEmptyGauge().DataPoints().AppendEmpty()
	if err := proc.ConsumeMetrics(context.Background(), metrics); err != nil {
		t.Fatalf("consume: %v", err)
	}
	if got := sink.DataPointCount(); got != 1 {
		t.Fatalf("next consumer saw %d items, want 1", got)
	}
}
