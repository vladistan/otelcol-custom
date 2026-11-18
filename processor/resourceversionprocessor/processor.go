// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package resourceversionprocessor

import (
	"context"
	"fmt"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/ptrace"
)

// stamper holds what the three signal processors share: the version attribute
// key, the host name option and the no-op lifecycle.
type stamper struct {
	config  *Config
	attrKey string
}

func newStamper(cfg *Config) stamper {
	return stamper{config: cfg, attrKey: fmt.Sprintf("collector.%s.version", cfg.Role)}
}

func (stamper) Capabilities() consumer.Capabilities {
	return consumer.Capabilities{MutatesData: true}
}

func (stamper) Start(context.Context, component.Host) error { return nil }

func (stamper) Shutdown(context.Context) error { return nil }

// stamp writes the version attribute and, when configured, shortens host.name.
func (s stamper) stamp(attrs pcommon.Map) {
	attrs.PutStr(s.attrKey, collectorVersion)
	if s.config.ShortHostName {
		shortenHostName(attrs)
	}
}

type logsProcessor struct {
	stamper
	nextConsumer consumer.Logs
}

func newLogsProcessor(cfg *Config, next consumer.Logs) *logsProcessor {
	return &logsProcessor{stamper: newStamper(cfg), nextConsumer: next}
}

func (p *logsProcessor) ConsumeLogs(ctx context.Context, ld plog.Logs) error {
	for i := 0; i < ld.ResourceLogs().Len(); i++ {
		p.stamp(ld.ResourceLogs().At(i).Resource().Attributes())
	}
	return p.nextConsumer.ConsumeLogs(ctx, ld)
}

type metricsProcessor struct {
	stamper
	nextConsumer consumer.Metrics
}

func newMetricsProcessor(cfg *Config, next consumer.Metrics) *metricsProcessor {
	return &metricsProcessor{stamper: newStamper(cfg), nextConsumer: next}
}

func (p *metricsProcessor) ConsumeMetrics(ctx context.Context, md pmetric.Metrics) error {
	for i := 0; i < md.ResourceMetrics().Len(); i++ {
		p.stamp(md.ResourceMetrics().At(i).Resource().Attributes())
	}
	return p.nextConsumer.ConsumeMetrics(ctx, md)
}

type tracesProcessor struct {
	stamper
	nextConsumer consumer.Traces
}

func newTracesProcessor(cfg *Config, next consumer.Traces) *tracesProcessor {
	return &tracesProcessor{stamper: newStamper(cfg), nextConsumer: next}
}

func (p *tracesProcessor) ConsumeTraces(ctx context.Context, td ptrace.Traces) error {
	for i := 0; i < td.ResourceSpans().Len(); i++ {
		p.stamp(td.ResourceSpans().At(i).Resource().Attributes())
	}
	return p.nextConsumer.ConsumeTraces(ctx, td)
}
