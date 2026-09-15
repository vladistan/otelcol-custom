// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package pgbouncerlogprocessor

import (
	"context"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/processor"
)

const (
	typeStr   = "pgbouncerlog"
	stability = component.StabilityLevelDevelopment
)

// NewFactory creates a new processor factory.
func NewFactory() processor.Factory {
	return processor.NewFactory(
		component.MustNewType(typeStr),
		createDefaultConfig,
		processor.WithLogs(createLogsProcessor, stability),
	)
}

func createDefaultConfig() component.Config {
	return &Config{
		SourceAttribute:  "",
		ServiceName:      "pgbouncer",
		SetServiceName:   true,
		ParseTimestamp:   true,
		PreserveOriginal: true,
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
