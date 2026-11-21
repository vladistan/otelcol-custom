// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package flowlogexporter

import (
	"context"
	"os"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/exporter"
	"go.opentelemetry.io/collector/exporter/exporterhelper"
)

const typeStr = "flowlog"

// NewFactory returns the factory the builder registers once the manifest lists this module.
func NewFactory() exporter.Factory {
	return exporter.NewFactory(
		component.MustNewType(typeStr),
		createDefaultConfig,
		exporter.WithLogs(createLogsExporter, component.StabilityLevelDevelopment),
	)
}

func createLogsExporter(ctx context.Context, set exporter.Settings, cfg component.Config) (exporter.Logs, error) {
	c := cfg.(*Config)
	return buildLogs(ctx, set, c, func(ctx context.Context) (conn, error) { return dial(ctx, c) })
}

// buildLogs wires the exporter, its queue and the queue guard; dial opens the connection.
func buildLogs(ctx context.Context, set exporter.Settings, c *Config, dial func(context.Context) (conn, error)) (exporter.Logs, error) {
	collector := c.Collector
	if collector == "" {
		collector = os.Getenv("HOSTNAME")
	}
	e, err := newLogsExporter(c, set, collector, dial)
	if err != nil {
		return nil, err
	}
	inner, err := exporterhelper.NewLogs(ctx, set, c, e.pushLogs,
		exporterhelper.WithStart(e.start),
		exporterhelper.WithShutdown(e.shutdown),
		exporterhelper.WithTimeout(c.TimeoutSettings),
		exporterhelper.WithQueue(c.QueueSettings),
	)
	if err != nil {
		return nil, err
	}
	return &queueGuard{Logs: inner, tel: e.tel}, nil
}
