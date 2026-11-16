// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package ipmireceiver

import (
	"context"
	"time"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/receiver"
)

const (
	typeStr   = "ipmi"
	stability = component.StabilityLevelDevelopment
)

// NewFactory creates the factory of the ipmi receiver.
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
		MaxConcurrent:      8,
		ExchangeTimeout:    3 * time.Second,
		ScrapeTimeout:      20 * time.Second,
	}
}

func createMetricsReceiver(
	_ context.Context,
	settings receiver.Settings,
	cfg component.Config,
	next consumer.Metrics,
) (receiver.Metrics, error) {
	typed := cfg.(*Config)
	if err := typed.Validate(); err != nil {
		return nil, err
	}
	return newReceiver(settings, typed, next, newDialer(typed.ExchangeTimeout)), nil
}
