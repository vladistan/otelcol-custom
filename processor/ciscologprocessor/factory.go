// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package ciscologprocessor

import (
	"context"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/processor"
)

const (
	typeStr = "ciscolog"

	defaultSourceAttribute = "original_message"
)

// NewFactory creates a new factory for the Cisco log processor.
func NewFactory() processor.Factory {
	return processor.NewFactory(
		component.MustNewType(typeStr),
		createDefaultConfig,
		processor.WithLogs(createLogsProcessor, component.StabilityLevelDevelopment),
	)
}

func createDefaultConfig() component.Config {
	return &Config{
		SourceAttribute: defaultSourceAttribute,
		SetHostname:     true,
		CleanBody:       true,
		RewriteMessages: true,
		AdjustSeverity:  true,
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
