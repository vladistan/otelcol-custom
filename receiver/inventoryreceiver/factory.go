// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package inventoryreceiver

import (
	"context"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/receiver"
)

const (
	typeStr   = "inventory"
	stability = component.StabilityLevelDevelopment
)

func NewFactory() receiver.Factory {
	return receiver.NewFactory(
		component.MustNewType(typeStr),
		createDefaultConfig,
		receiver.WithMetrics(createMetricsReceiver, stability),
	)
}

func createDefaultConfig() component.Config {
	return &Config{CollectionInterval: defaultInterval, MaxDrives: defaultDrives}
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
	src, err := newSource(typed)
	if err != nil {
		return nil, err
	}
	return newReceiver(settings, typed, next, src), nil
}
