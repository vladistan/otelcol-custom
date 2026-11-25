// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package hwmapprocessor

import (
	"context"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/processor"
)

const (
	typeStr   = "hwmap"
	stability = component.StabilityLevelDevelopment
)

// NewFactory returns the factory the builder registers once the manifest lists this module.
func NewFactory() processor.Factory {
	return processor.NewFactory(
		component.MustNewType(typeStr),
		func() component.Config { return &Config{} },
		processor.WithMetrics(createMetricsProcessor, stability),
	)
}

type hwmap struct {
	*engine
	next consumer.Metrics
}

func createMetricsProcessor(_ context.Context, set processor.Settings, cfg component.Config, next consumer.Metrics) (processor.Metrics, error) {
	e, err := newEngine(cfg.(*Config), set.TelemetrySettings)
	if err != nil {
		return nil, err
	}
	return &hwmap{engine: e, next: next}, nil
}

func (h *hwmap) Start(context.Context, component.Host) error { return nil }

func (h *hwmap) Shutdown(context.Context) error { return nil }

func (h *hwmap) Capabilities() consumer.Capabilities { return consumer.Capabilities{MutatesData: true} }

func (h *hwmap) ConsumeMetrics(ctx context.Context, md pmetric.Metrics) error {
	h.process(ctx, md)
	return h.next.ConsumeMetrics(ctx, md)
}
