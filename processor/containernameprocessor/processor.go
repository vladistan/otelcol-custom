// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package containernameprocessor

import (
	"context"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
)

const (
	containerNameAttr = "container.name"
	serviceNameAttr   = "service.name"
)

type processorImpl struct {
	next consumer.Logs
}

func (*processorImpl) Start(context.Context, component.Host) error { return nil }

func (*processorImpl) Shutdown(context.Context) error { return nil }

func (*processorImpl) Capabilities() consumer.Capabilities {
	return consumer.Capabilities{MutatesData: true}
}

// ConsumeLogs sets service.name from container.name on every resource that has a
// non-empty string container.name, replacing the journald unit name.
func (p *processorImpl) ConsumeLogs(ctx context.Context, ld plog.Logs) error {
	for i := 0; i < ld.ResourceLogs().Len(); i++ {
		setServiceNameFromContainer(ld.ResourceLogs().At(i).Resource().Attributes())
	}
	return p.next.ConsumeLogs(ctx, ld)
}

func setServiceNameFromContainer(attrs pcommon.Map) {
	v, ok := attrs.Get(containerNameAttr)
	if !ok || v.Type() != pcommon.ValueTypeStr || v.Str() == "" {
		return
	}
	attrs.PutStr(serviceNameAttr, v.Str())
}
