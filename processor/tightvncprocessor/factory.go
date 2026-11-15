// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package tightvncprocessor

import (
	"context"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/processor"
)

const (
	typeStr   = "tightvnc"
	stability = component.StabilityLevelDevelopment
)

// NewFactory returns the factory the builder registers once the Windows manifest lists this module.
func NewFactory() processor.Factory {
	return processor.NewFactory(
		component.MustNewType(typeStr),
		createDefaultConfig,
		processor.WithLogs(createLogsProcessor, stability),
	)
}

func createDefaultConfig() component.Config {
	return &Config{
		TimeZone: "Local",
		// "Recieved" is the server's own spelling; the prefix has to match the log text.
		DropPrefixes: []string{
			"Control client", "Recieved control message", "process memory usage",
			"new client, process memory usage", "Shared flag", "Initial view-only state",
			"RFB initialization phase 1 completed", "Requested to start up extra RFB servers",
			"Requested to shut down extra RFB servers", "Considering to reload extra RFB servers",
			"Same Extra Ports configuration", "Timeout on acquire frame", "DesktopSrvDispatcher",
			"View port", "Constrained", "Active console session Id", "Destination dirty rect",
			"update detected", "Acquire frame", "get frame buffer", "Time between",
			"UpdateFilter", "WinVideoRegionUpdaterImpl",
		},
	}
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
	loc, err := conf.location()
	if err != nil {
		return nil, err
	}
	tel, err := newTelemetry(set.TelemetrySettings)
	if err != nil {
		return nil, err
	}
	return &processorImpl{prefixes: conf.DropPrefixes, loc: loc, next: nextConsumer, tel: tel}, nil
}
