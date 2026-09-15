// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package assetdbprocessor

import (
	"context"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/processor"
)

const (
	typeStr = "assetdb"

	// Default attribute name for MAC address lookup (set by macextractorprocessor)
	defaultSourceAttribute = "client.mac"
)

// NewFactory creates a factory for the assetdb processor.
func NewFactory() processor.Factory {
	return processor.NewFactory(
		component.MustNewType(typeStr),
		createDefaultConfig,
		processor.WithLogs(createLogsProcessor, component.StabilityLevelDevelopment),
		processor.WithMetrics(createMetricsProcessor, component.StabilityLevelDevelopment),
		processor.WithTraces(createTracesProcessor, component.StabilityLevelDevelopment),
	)
}

func createDefaultConfig() component.Config {
	return &Config{
		SourceAttribute: defaultSourceAttribute,
	}
}

func createLogsProcessor(
	ctx context.Context,
	set processor.Settings,
	cfg component.Config,
	nextConsumer consumer.Logs,
) (processor.Logs, error) {
	c := cfg.(*Config)
	return newLogsProcessor(set, c, nextConsumer)
}

func createMetricsProcessor(
	ctx context.Context,
	set processor.Settings,
	cfg component.Config,
	nextConsumer consumer.Metrics,
) (processor.Metrics, error) {
	c := cfg.(*Config)
	return newMetricsProcessor(set, c, nextConsumer)
}

func createTracesProcessor(
	ctx context.Context,
	set processor.Settings,
	cfg component.Config,
	nextConsumer consumer.Traces,
) (processor.Traces, error) {
	c := cfg.(*Config)
	return newTracesProcessor(set, c, nextConsumer)
}
