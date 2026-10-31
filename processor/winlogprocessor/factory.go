// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package winlogprocessor

import (
	"context"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/processor"
)

const (
	typeStr   = "winlog"
	stability = component.StabilityLevelDevelopment

	defaultOriginalMessageMaxBytes = 64 * 1024
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
	return &Config{DropRules: DefaultDropRules(), OriginalMessageMaxBytes: defaultOriginalMessageMaxBytes}
}

func createLogsProcessor(
	_ context.Context,
	set processor.Settings,
	cfg component.Config,
	nextConsumer consumer.Logs,
) (processor.Logs, error) {
	config := cfg.(*Config)
	rules, err := compileRules(config.DropRules)
	if err != nil {
		return nil, err
	}
	tel, err := newTelemetry(set.TelemetrySettings)
	if err != nil {
		return nil, err
	}
	p := &winlogProcessor{
		next:             nextConsumer,
		rules:            rules,
		tel:              tel,
		maxOriginalBytes: config.OriginalMessageMaxBytes,
		logger:           set.Logger,
	}
	if config.DropSummary.Enabled {
		p.summary = newDropSummary()
		p.rules.needsID = true
	}
	return p, nil
}
