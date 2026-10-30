// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package auth0receiver

import (
	"context"
	"time"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/receiver"
)

const (
	typeStr   = "auth0"
	stability = component.StabilityLevelDevelopment
)

// NewFactory returns the factory the builder registers once the manifest lists this module.
func NewFactory() receiver.Factory {
	return receiver.NewFactory(
		component.MustNewType(typeStr),
		createDefaultConfig,
		receiver.WithLogs(createLogsReceiver, stability),
	)
}

func createDefaultConfig() component.Config {
	return &Config{
		SourcesSuffix:       []string{"auth0.logs", "auth0.events"},
		WaitTimeSeconds:     maxWaitTimeSeconds,
		MaxNumberOfMessages: maxNumberOfMessagesCap,
		ErrorBackoff:        5 * time.Second,
	}
}

func createLogsReceiver(
	_ context.Context,
	set receiver.Settings,
	cfg component.Config,
	next consumer.Logs,
) (receiver.Logs, error) {
	return newReceiver(cfg.(*Config), set, next, nil)
}
