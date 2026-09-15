// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package tektonlogprocessor

import (
	"context"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/processor"
)

const (
	typeStr   = "tektonlog"
	stability = component.StabilityLevelAlpha
)

// NewFactory creates a new factory for the tektonlog processor.
func NewFactory() processor.Factory {
	return processor.NewFactory(
		component.MustNewType(typeStr),
		createDefaultConfig,
		processor.WithLogs(createLogsProcessor, stability),
	)
}

func createDefaultConfig() component.Config {
	return &Config{
		ServiceName:     "controller",
		RewriteMessages: true,
	}
}

func createLogsProcessor(
	ctx context.Context,
	set processor.Settings,
	cfg component.Config,
	nextConsumer consumer.Logs,
) (processor.Logs, error) {
	processorCfg := cfg.(*Config)
	return newProcessor(set.Logger, processorCfg, nextConsumer), nil
}
