// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package nginxlogprocessor

import (
	"context"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/processor"
)

const (
	typeStr = "nginxlog"
)

// NewFactory creates a new factory for the Nginx log processor.
func NewFactory() processor.Factory {
	return processor.NewFactory(
		component.MustNewType(typeStr),
		createDefaultConfig,
		processor.WithLogs(createLogsProcessor, component.StabilityLevelDevelopment),
	)
}

func createDefaultConfig() component.Config {
	return &Config{
		SourceAttribute:  "",
		SetServiceName:   true,
		ServiceName:      "nginx",
		ParseTimestamp:   false,
		PreserveOriginal: false,
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
