// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package resourceversionprocessor

import (
	"context"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/processor"
)

const (
	typeStr = "resourceversion"
)

// collectorVersion is set by the main package at startup
var collectorVersion = "unknown"

// SetCollectorVersion sets the collector version (called from main.go)
func SetCollectorVersion(version string) {
	collectorVersion = version
}

// NewFactory creates a factory for the resourceversion processor.
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
		Role: "edge",
	}
}

func createLogsProcessor(
	ctx context.Context,
	set processor.Settings,
	cfg component.Config,
	nextConsumer consumer.Logs,
) (processor.Logs, error) {
	c := cfg.(*Config)
	return newLogsProcessor(set, c, nextConsumer), nil
}

func createMetricsProcessor(
	ctx context.Context,
	set processor.Settings,
	cfg component.Config,
	nextConsumer consumer.Metrics,
) (processor.Metrics, error) {
	c := cfg.(*Config)
	return newMetricsProcessor(set, c, nextConsumer), nil
}

func createTracesProcessor(
	ctx context.Context,
	set processor.Settings,
	cfg component.Config,
	nextConsumer consumer.Traces,
) (processor.Traces, error) {
	c := cfg.(*Config)
	return newTracesProcessor(set, c, nextConsumer), nil
}
