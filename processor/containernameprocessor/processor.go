// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package containernameprocessor

import (
	"context"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/ptrace"
)

const (
	containerNameAttr = "container.name"
	serviceNameAttr   = "service.name"
)

// base holds what the three signal processors share.
type base struct{}

func (base) Start(context.Context, component.Host) error { return nil }

func (base) Shutdown(context.Context) error { return nil }

func (base) Capabilities() consumer.Capabilities {
	return consumer.Capabilities{MutatesData: true}
}

type processorImpl struct {
	base
	next consumer.Logs
}

// ConsumeLogs sets service.name from container.name on every resource that has a
// non-empty string container.name, replacing the journald unit name.
func (p *processorImpl) ConsumeLogs(ctx context.Context, ld plog.Logs) error {
	for i := 0; i < ld.ResourceLogs().Len(); i++ {
		setServiceNameFromContainer(ld.ResourceLogs().At(i).Resource().Attributes())
	}
	return p.next.ConsumeLogs(ctx, ld)
}

type metricsImpl struct {
	base
	next consumer.Metrics
}

// ConsumeMetrics applies the same rule to the resources of metrics, such as the
// container.* series of docker_stats.
func (p *metricsImpl) ConsumeMetrics(ctx context.Context, md pmetric.Metrics) error {
	for i := 0; i < md.ResourceMetrics().Len(); i++ {
		setServiceNameFromContainer(md.ResourceMetrics().At(i).Resource().Attributes())
	}
	return p.next.ConsumeMetrics(ctx, md)
}

type tracesImpl struct {
	base
	next consumer.Traces
}

// ConsumeTraces applies the same rule to the resources of traces.
func (p *tracesImpl) ConsumeTraces(ctx context.Context, td ptrace.Traces) error {
	for i := 0; i < td.ResourceSpans().Len(); i++ {
		setServiceNameFromContainer(td.ResourceSpans().At(i).Resource().Attributes())
	}
	return p.next.ConsumeTraces(ctx, td)
}

func setServiceNameFromContainer(attrs pcommon.Map) {
	v, ok := attrs.Get(containerNameAttr)
	if !ok || v.Type() != pcommon.ValueTypeStr || v.Str() == "" {
		return
	}
	attrs.PutStr(serviceNameAttr, v.Str())
}
