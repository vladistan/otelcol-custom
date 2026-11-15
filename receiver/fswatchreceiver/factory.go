// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package fswatchreceiver

import (
	"context"
	"time"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/receiver"
)

const (
	typeStr   = "fswatch"
	stability = component.StabilityLevelAlpha
)

func NewFactory() receiver.Factory {
	return receiver.NewFactory(
		component.MustNewType(typeStr),
		createDefaultConfig,
		receiver.WithLogs(createLogsReceiver, stability),
	)
}

func createDefaultConfig() component.Config {
	return &Config{
		StartAt:       startAtEnd,
		Settle:        5 * time.Minute,
		SettleTimeout: 10 * time.Minute,
		MaxCatchup:    168 * time.Hour,
		BufferSize:    256 * 1024,
		MaxRescans:    6,
	}
}

func createLogsReceiver(
	_ context.Context,
	settings receiver.Settings,
	cfg component.Config,
	next consumer.Logs,
) (receiver.Logs, error) {
	conf := cfg.(*Config)
	if err := conf.Validate(); err != nil {
		return nil, err
	}
	return newReceiver(settings, conf, next)
}
