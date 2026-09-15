// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package containerdlogprocessor

import (
	"context"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/processor"
)

const (
	typeStr   = "containerdlog"
	stability = component.StabilityLevelDevelopment
)

// NewFactory creates a factory for the containerd log processor.
func NewFactory() processor.Factory {
	return processor.NewFactory(
		component.MustNewType(typeStr),
		createDefaultConfig,
		processor.WithLogs(createLogsProcessor, stability),
	)
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
