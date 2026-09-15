// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package macextractorprocessor

import (
	"context"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/processor"
)

const (
	typeStr = "macextractor"

	// ECS semantic convention for client system
	defaultTargetAttribute = "client.mac"
)

// NewFactory creates a new factory for the MAC extractor processor.
func NewFactory() processor.Factory {
	return processor.NewFactory(
		component.MustNewType(typeStr),
		createDefaultConfig,
		processor.WithLogs(createLogsProcessor, component.StabilityLevelDevelopment),
	)
}

func createDefaultConfig() component.Config {
	return &Config{
		TargetAttribute:    defaultTargetAttribute,
		SearchBody:         true,
		AttributesToSearch: []string{"original_message"},
		ExtractAll:         false,
	}
}

func createLogsProcessor(
	ctx context.Context,
	set processor.Settings,
	cfg component.Config,
	nextConsumer consumer.Logs,
) (processor.Logs, error) {
	processorCfg := cfg.(*Config)
	return &logsProcessor{
		config:       processorCfg,
		logger:       set.Logger,
		nextConsumer: nextConsumer,
	}, nil
}
