// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package resourceversionprocessor

import (
	"context"
	"fmt"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"go.opentelemetry.io/collector/processor"
)

// logsProcessor adds collector info to logs
type logsProcessor struct {
	config       *Config
	nextConsumer consumer.Logs
	attrKey      string
}

func newLogsProcessor(set processor.Settings, cfg *Config, next consumer.Logs) *logsProcessor {
	return &logsProcessor{
		config:       cfg,
		nextConsumer: next,
		attrKey:      fmt.Sprintf("collector.%s.version", cfg.Role),
	}
}

func (p *logsProcessor) Capabilities() consumer.Capabilities {
	return consumer.Capabilities{MutatesData: true}
}

func (p *logsProcessor) Start(ctx context.Context, host component.Host) error {
	return nil
}

func (p *logsProcessor) Shutdown(ctx context.Context) error {
	return nil
}

func (p *logsProcessor) ConsumeLogs(ctx context.Context, ld plog.Logs) error {
	for i := 0; i < ld.ResourceLogs().Len(); i++ {
		rl := ld.ResourceLogs().At(i)
		rl.Resource().Attributes().PutStr(p.attrKey, collectorVersion)
	}
	return p.nextConsumer.ConsumeLogs(ctx, ld)
}

// metricsProcessor adds collector info to metrics
type metricsProcessor struct {
	config       *Config
	nextConsumer consumer.Metrics
	attrKey      string
}

func newMetricsProcessor(set processor.Settings, cfg *Config, next consumer.Metrics) *metricsProcessor {
	return &metricsProcessor{
		config:       cfg,
		nextConsumer: next,
		attrKey:      fmt.Sprintf("collector.%s.version", cfg.Role),
	}
}

func (p *metricsProcessor) Capabilities() consumer.Capabilities {
	return consumer.Capabilities{MutatesData: true}
}

func (p *metricsProcessor) Start(ctx context.Context, host component.Host) error {
	return nil
}

func (p *metricsProcessor) Shutdown(ctx context.Context) error {
	return nil
}

func (p *metricsProcessor) ConsumeMetrics(ctx context.Context, md pmetric.Metrics) error {
	for i := 0; i < md.ResourceMetrics().Len(); i++ {
		rm := md.ResourceMetrics().At(i)
		rm.Resource().Attributes().PutStr(p.attrKey, collectorVersion)
	}
	return p.nextConsumer.ConsumeMetrics(ctx, md)
}

// tracesProcessor adds collector info to traces
type tracesProcessor struct {
	config       *Config
	nextConsumer consumer.Traces
	attrKey      string
}

func newTracesProcessor(set processor.Settings, cfg *Config, next consumer.Traces) *tracesProcessor {
	return &tracesProcessor{
		config:       cfg,
		nextConsumer: next,
		attrKey:      fmt.Sprintf("collector.%s.version", cfg.Role),
	}
}

func (p *tracesProcessor) Capabilities() consumer.Capabilities {
	return consumer.Capabilities{MutatesData: true}
}

func (p *tracesProcessor) Start(ctx context.Context, host component.Host) error {
	return nil
}

func (p *tracesProcessor) Shutdown(ctx context.Context) error {
	return nil
}

func (p *tracesProcessor) ConsumeTraces(ctx context.Context, td ptrace.Traces) error {
	for i := 0; i < td.ResourceSpans().Len(); i++ {
		rs := td.ResourceSpans().At(i)
		rs.Resource().Attributes().PutStr(p.attrKey, collectorVersion)
	}
	return p.nextConsumer.ConsumeTraces(ctx, td)
}
