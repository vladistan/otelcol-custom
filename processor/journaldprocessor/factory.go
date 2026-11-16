// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package journaldprocessor

import (
	"context"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/processor"
)

const (
	typeStr   = "journald"
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

func createDefaultConfig() component.Config {
	return &Config{
		SetResource:     true,
		MapSeverity:     true,
		BodyFromMessage: true,
		SplitResources:  true,
		KeepFields:      true,
	}
}

func createLogsProcessor(
	_ context.Context,
	set processor.Settings,
	cfg component.Config,
	nextConsumer consumer.Logs,
) (processor.Logs, error) {
	resources, err := newResourcesPerBatch(set.TelemetrySettings)
	if err != nil {
		return nil, err
	}
	conf := cfg.(*Config)
	if conf.MapExtended && !conf.SplitResources {
		set.Logger.Warn("map_extended without split_resources: records of different units that share a resource get the resource attributes of one record")
	}
	return newJournaldProcessor(conf, nextConsumer, resources), nil
}
