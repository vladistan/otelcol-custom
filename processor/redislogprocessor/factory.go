// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package redislogprocessor

import (
	"context"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/processor"
)

const (
	typeStr = "redislog"
)

// NewFactory creates a factory for the Redis log processor.
func NewFactory() processor.Factory {
	return processor.NewFactory(
		component.MustNewType(typeStr),
		createDefaultConfig,
		processor.WithLogs(createLogsProcessor, component.StabilityLevelAlpha),
	)
}

func createDefaultConfig() component.Config {
	return &Config{
		ServiceName:      "redis",
		SourceAttribute:  "",
		SetServiceName:   true,
		PreserveOriginal: true,
		RewriteMessages:  true,
		AdjustSeverity:   true,
	}
}

func createLogsProcessor(
	ctx context.Context,
	set processor.Settings,
	cfg component.Config,
	next consumer.Logs,
) (processor.Logs, error) {
	pCfg := cfg.(*Config)
	return &logsProcessor{
		config:       pCfg,
		logger:       set.Logger,
		nextConsumer: next,
	}, nil
}
