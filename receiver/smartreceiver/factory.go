// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package smartreceiver

import (
	"context"
	"time"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/receiver"
)

const (
	typeStr   = "smart"
	stability = component.StabilityLevelDevelopment
)

// NewFactory returns the factory the builder registers once the manifest lists this module.
func NewFactory() receiver.Factory {
	return receiver.NewFactory(
		component.MustNewType(typeStr),
		createDefaultConfig,
		receiver.WithMetrics(createMetricsReceiver, stability),
	)
}

func createDefaultConfig() component.Config {
	return &Config{
		CollectionInterval: 5 * time.Minute,
		CommandTimeout:     5 * time.Second,
		SkipIfStandby:      true,
		ValueTypes:         []string{"normalized", "raw"},
		MaxDevices:         64,
	}
}

func createMetricsReceiver(
	_ context.Context,
	settings receiver.Settings,
	cfg component.Config,
	next consumer.Metrics,
) (receiver.Metrics, error) {
	rcfg := cfg.(*Config)
	if err := rcfg.Validate(); err != nil {
		return nil, err
	}
	skipped, err := newSkippedCounter(settings.TelemetrySettings)
	if err != nil {
		return nil, err
	}
	b, err := newSystemBus()
	if err != nil {
		return nil, err
	}
	return newReceiver(settings, rcfg, next, b, skipped, firstReadDelay)
}
