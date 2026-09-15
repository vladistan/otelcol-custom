// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logruslogprocessor

import (
	"context"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/processor"
)

const (
	typeStr   = "logruslog"
	stability = component.StabilityLevelDevelopment
)

// NewFactory creates a new factory for the logruslog processor.
func NewFactory() processor.Factory {
	return processor.NewFactory(
		component.MustNewType(typeStr),
		createDefaultConfig,
		processor.WithLogs(createLogsProcessor, stability),
	)
}

func createDefaultConfig() component.Config {
	return &Config{
		ServiceName:     "",
		RewriteMessages: true,
		AdjustSeverity:  true,
		ParseTimestamp:  false,
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
