// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package sourcetagprocessor

import (
	"context"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/plog"
)

type processorImpl struct {
	provider string
	rules    rules
	next     consumer.Logs
	tel      *telemetry
}

func (p *processorImpl) Start(context.Context, component.Host) error { return nil }

func (p *processorImpl) Shutdown(context.Context) error { return nil }

func (p *processorImpl) Capabilities() consumer.Capabilities {
	return consumer.Capabilities{MutatesData: true}
}

func (p *processorImpl) ConsumeLogs(ctx context.Context, ld plog.Logs) error {
	var unsourced, uncategorized int64
	for i := 0; i < ld.ResourceLogs().Len(); i++ {
		rl := ld.ResourceLogs().At(i)
		res := rl.Resource().Attributes()
		p.rules.resource(res)
		for j := 0; j < rl.ScopeLogs().Len(); j++ {
			sl := rl.ScopeLogs().At(j)
			records := sl.LogRecords()
			for k := 0; k < records.Len(); k++ {
				lr := records.At(k)
				p.rules.record(res, sl.Scope().Attributes(), lr)
				if !has(lr.Attributes(), sourceAttr) {
					unsourced++
				}
				if !has(lr.Attributes(), categoryAttr) {
					uncategorized++
				}
			}
		}
	}
	p.tel.add(ctx, p.provider, unsourced, uncategorized)
	return p.next.ConsumeLogs(ctx, ld)
}
