// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package ethtoolreceiver

import (
	"context"
	"time"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/receiver"
)

const (
	typeStr   = "ethtool"
	stability = component.StabilityLevelDevelopment
	// defaultMaxStatistics bounds network.interface.statistic series per scrape.
	defaultMaxStatistics = 200
)

// NewFactory creates the factory of the ethtool receiver.
func NewFactory() receiver.Factory {
	return receiver.NewFactory(
		component.MustNewType(typeStr),
		createDefaultConfig,
		receiver.WithMetrics(createMetricsReceiver, stability),
	)
}

func createDefaultConfig() component.Config {
	return &Config{
		CollectionInterval: time.Minute,
		Drivers:            []string{"ena"},
		MaxStatistics:      defaultMaxStatistics,
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
	nic, err := newNIC()
	if err != nil {
		return nil, err
	}
	return newReceiver(settings, rcfg, next, nic), nil
}
