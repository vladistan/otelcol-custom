// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package kthreadprocessor

import (
	"context"
	"slices"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/processor"
	"go.uber.org/zap"
)

const (
	typeStr   = "kthread"
	stability = component.StabilityLevelDevelopment
)

// NewFactory returns the factory the builder registers once the manifest lists this module.
func NewFactory() processor.Factory {
	return processor.NewFactory(
		component.MustNewType(typeStr),
		createDefaultConfig,
		processor.WithMetrics(createMetricsProcessor, stability),
	)
}

func createDefaultConfig() component.Config {
	return &Config{Patterns: slices.Clone(defaultPatterns), AggregateName: defaultAggregateName}
}

func createMetricsProcessor(
	_ context.Context,
	set processor.Settings,
	cfg component.Config,
	next consumer.Metrics,
) (processor.Metrics, error) {
	c := cfg.(*Config)
	p, err := newProcessor(c, next)
	if err != nil {
		return nil, err
	}
	if catchAll := c.catchAllPatterns(); len(catchAll) > 0 {
		set.Logger.Warn("kthread pattern matches every process; all process metrics merge into one resource",
			zap.Strings("patterns", catchAll))
	}
	return p, nil
}
