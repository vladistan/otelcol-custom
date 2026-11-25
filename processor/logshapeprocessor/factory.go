// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"context"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/processor"
)

const typeStr = "logshape"

// NewFactory creates the logshape processor factory.
func NewFactory() processor.Factory {
	return processor.NewFactory(
		component.MustNewType(typeStr),
		createDefaultConfig,
		processor.WithLogs(createLogs, component.StabilityLevelDevelopment),
	)
}

func createDefaultConfig() component.Config {
	return &Config{
		Branches: append([]string(nil), branchNames...),
		Syslog: SyslogConfig{
			SourceAttribute: "message", SetHostname: true, SetServiceName: true,
			MapSeverity: true, PreserveOriginal: true, CleanBody: true,
		},
		Cisco: CiscoConfig{
			SourceAttribute: originalKey, SetHostname: true, CleanBody: true,
			RewriteMessages: true, AdjustSeverity: true,
		},
		Logrus:   LogrusConfig{RewriteMessages: true, AdjustSeverity: true, ParseTimestamp: true},
		Postgres: PostgresConfig{RewriteMessages: true, ExtractDetails: true, AdjustSeverity: true},
		Redis: RedisConfig{
			ServiceName: "redis", SetServiceName: true, PreserveOriginal: true,
			RewriteMessages: true, AdjustSeverity: true,
		},
		Uvicorn:    UvicornConfig{ExtractHTTPFields: true, RewriteMessages: true, AdjustSeverity: true},
		Nginx:      NginxConfig{SetServiceName: true, ServiceName: "nginx", ParseTimestamp: true},
		Autoscaler: AutoscalerConfig{KeepRatio: 0.2},
		Sentry: SentryConfig{
			SetServiceName: true, ServiceName: "sentry", ParseTimestamp: true,
			PreserveOriginal: true, ExtractHTTPFields: true,
		},
	}
}

func createLogs(_ context.Context, set processor.Settings, cfg component.Config, next consumer.Logs) (processor.Logs, error) {
	return newProcessor(set, cfg.(*Config), next)
}
