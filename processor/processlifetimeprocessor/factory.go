// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package processlifetimeprocessor

import (
	"context"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/processor"
)

const (
	typeStr   = "processlifetime"
	stability = component.StabilityLevelDevelopment
)

// NewFactory returns the factory the builder registers once the manifest lists this module.
func NewFactory() processor.Factory {
	return processor.NewFactory(
		component.MustNewType(typeStr),
		createDefaultConfig,
		processor.WithMetrics(createMetricsProcessor, stability),
	)
}

func createDefaultConfig() component.Config { return &Config{} }

func createMetricsProcessor(
	_ context.Context,
	_ processor.Settings,
	_ component.Config,
	next consumer.Metrics,
) (processor.Metrics, error) {
	return newProcessor(next), nil
}
