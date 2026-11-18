// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package containernameprocessor

import (
	"context"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/processor"
)

const (
	typeStr   = "containername"
	stability = component.StabilityLevelDevelopment
)

// NewFactory returns the factory the builder registers once the manifest lists this module.
func NewFactory() processor.Factory {
	return processor.NewFactory(
		component.MustNewType(typeStr),
		createDefaultConfig,
		processor.WithLogs(createLogsProcessor, stability),
		processor.WithMetrics(createMetricsProcessor, stability),
		processor.WithTraces(createTracesProcessor, stability),
	)
}

func createDefaultConfig() component.Config { return &Config{} }

func createLogsProcessor(
	_ context.Context,
	_ processor.Settings,
	_ component.Config,
	nextConsumer consumer.Logs,
) (processor.Logs, error) {
	return &processorImpl{next: nextConsumer}, nil
}

func createMetricsProcessor(
	_ context.Context,
	_ processor.Settings,
	_ component.Config,
	nextConsumer consumer.Metrics,
) (processor.Metrics, error) {
	return &metricsImpl{next: nextConsumer}, nil
}

func createTracesProcessor(
	_ context.Context,
	_ processor.Settings,
	_ component.Config,
	nextConsumer consumer.Traces,
) (processor.Traces, error) {
	return &tracesImpl{next: nextConsumer}, nil
}
