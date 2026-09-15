// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logrecombineprocessor

import (
	"context"
	"time"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/processor"
)

const (
	typeStr = "logrecombine"
)

// NewFactory creates a factory for the logrecombine processor.
func NewFactory() processor.Factory {
	return processor.NewFactory(
		component.MustNewType(typeStr),
		createDefaultConfig,
		processor.WithLogs(createLogsProcessor, component.StabilityLevelDevelopment),
	)
}

func createDefaultConfig() component.Config {
	return &Config{
		CombineField:        "body.MESSAGE",
		SourceIdentifier:    "body._PID",
		IsFirstEntryPattern: `^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}`,
		CombineWith:         "\n",
		GapTimeout:          10 * time.Millisecond,
		MaxTimeout:          80 * time.Millisecond,
		MaxBatchSize:        50,
		MaxSources:          1000,
	}
}

func createLogsProcessor(
	ctx context.Context,
	set processor.Settings,
	cfg component.Config,
	nextConsumer consumer.Logs,
) (processor.Logs, error) {
	c := cfg.(*Config)
	return newLogsProcessor(set, c, nextConsumer)
}
