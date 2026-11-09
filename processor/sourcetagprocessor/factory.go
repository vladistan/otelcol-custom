// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package sourcetagprocessor

import (
	"context"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/processor"
)

const (
	typeStr   = "sourcetag"
	stability = component.StabilityLevelDevelopment
)

// NewFactory returns the factory the builder registers once the manifest lists this module.
func NewFactory() processor.Factory {
	return processor.NewFactory(
		component.MustNewType(typeStr),
		func() component.Config { return &Config{} },
		processor.WithLogs(createLogsProcessor, stability),
	)
}

func createLogsProcessor(
	_ context.Context,
	set processor.Settings,
	cfg component.Config,
	nextConsumer consumer.Logs,
) (processor.Logs, error) {
	conf := cfg.(*Config)
	if err := conf.Validate(); err != nil {
		return nil, err
	}
	tel, err := newTelemetry(set.TelemetrySettings)
	if err != nil {
		return nil, err
	}
	rules, err := rulesFor(conf.Provider)
	if err != nil {
		return nil, err
	}
	return &processorImpl{provider: conf.Provider, rules: rules, next: nextConsumer, tel: tel}, nil
}
