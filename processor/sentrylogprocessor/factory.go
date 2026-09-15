// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package sentrylogprocessor

import (
	"context"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/processor"
)

const (
	typeStr   = "sentrylog"
	stability = component.StabilityLevelAlpha
)

// NewFactory creates a factory for the Sentry log processor.
func NewFactory() processor.Factory {
	return processor.NewFactory(
		component.MustNewType(typeStr),
		createDefaultConfig,
		processor.WithLogs(createLogsProcessor, stability),
	)
}

func createDefaultConfig() component.Config {
	return &Config{
		SourceAttribute:   "",
		SetServiceName:    true,
		ServiceName:       "sentry",
		ParseTimestamp:    false,
		PreserveOriginal:  false,
		ExtractHTTPFields: true,
	}
}

func createLogsProcessor(
	ctx context.Context,
	set processor.Settings,
	cfg component.Config,
	nextConsumer consumer.Logs,
) (processor.Logs, error) {
	pCfg := cfg.(*Config)
	return &logsProcessor{
		config:       pCfg,
		logger:       set.Logger,
		nextConsumer: nextConsumer,
	}, nil
}
