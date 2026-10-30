// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package healthprobesampler

import (
	"context"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/processor"

	"github.com/vladistan/otelcol-custom/processor/internal/probesample"
)

const (
	typeStr   = "healthprobe"
	stability = component.StabilityLevelDevelopment
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
	return &Config{
		Config:          probesample.Default(),
		Enabled:         true,
		StatusAttribute: "http.response.status_code",
		PathAttribute:   "url.path",
		AgentAttribute:  "user_agent.original",
	}
}

func createLogsProcessor(
	_ context.Context,
	set processor.Settings,
	cfg component.Config,
	nextConsumer consumer.Logs,
) (processor.Logs, error) {
	return newProcessor(cfg.(*Config), set.TelemetrySettings, nextConsumer, nil)
}
