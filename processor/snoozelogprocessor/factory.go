// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package snoozelogprocessor

import (
	"context"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/processor"
)

const (
	typeStr   = "snoozelog"
	stability = component.StabilityLevelDevelopment
)

// NewFactory returns the factory the builder registers once the manifest lists this module.
func NewFactory() processor.Factory {
	return processor.NewFactory(
		component.MustNewType(typeStr),
		createDefaultConfig,
		processor.WithLogs(createLogsProcessor, stability),
	)
}

// createDefaultConfig returns no rules: match is required and Validate rejects an empty list.
func createDefaultConfig() component.Config {
	return &Config{}
}

func createLogsProcessor(
	_ context.Context,
	_ processor.Settings,
	cfg component.Config,
	nextConsumer consumer.Logs,
) (processor.Logs, error) {
	conf := cfg.(*Config)
	if err := conf.Validate(); err != nil {
		return nil, err
	}
	return &snoozeProcessor{rules: conf.Match, next: nextConsumer}, nil
}
