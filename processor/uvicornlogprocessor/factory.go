// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package uvicornlogprocessor

import (
	"context"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/processor"
)

const (
	typeStr   = "uvicornlog"
	stability = component.StabilityLevelDevelopment
)

// NewFactory creates a new factory for the uvicornlog processor.
func NewFactory() processor.Factory {
	return processor.NewFactory(
		component.MustNewType(typeStr),
		createDefaultConfig,
		processor.WithLogs(createLogsProcessor, stability),
	)
}

func createDefaultConfig() component.Config {
	return &Config{
		ServiceName:       "uvicorn",
		RewriteMessages:   true,
		ExtractHTTPFields: true,
		AdjustSeverity:    true,
	}
}

func createLogsProcessor(
	ctx context.Context,
	set processor.Settings,
	cfg component.Config,
	nextConsumer consumer.Logs,
) (processor.Logs, error) {
	return newProcessor(set.Logger, cfg.(*Config), nextConsumer), nil
}
