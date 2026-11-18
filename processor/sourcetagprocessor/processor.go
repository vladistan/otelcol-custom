// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package sourcetagprocessor

import (
	"context"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
)

type processorImpl struct {
	provider string
	rules    rules
	next     consumer.Logs
	metrics  *telemetry
}

func (p *processorImpl) Start(context.Context, component.Host) error { return nil }

func (p *processorImpl) Shutdown(context.Context) error { return nil }

func (p *processorImpl) Capabilities() consumer.Capabilities {
	return consumer.Capabilities{MutatesData: true}
}

func (p *processorImpl) ConsumeLogs(ctx context.Context, ld plog.Logs) error {
	var counts missing
	for i := 0; i < ld.ResourceLogs().Len(); i++ {
		rl := ld.ResourceLogs().At(i)
		res := rl.Resource().Attributes()
		p.rules.resource(res)
		source, mixed := p.tagRecords(rl, res, &counts)
		// One name for the resource only when every sourced record agrees on the source.
		if name, ok := p.rules.services[source]; ok && !mixed && !hasValue(res, cloudService) {
			res.PutStr(cloudService, name)
		}
	}
	p.metrics.add(ctx, p.provider, counts.unsourced, counts.uncategorized)
	return p.next.ConsumeLogs(ctx, ld)
}

// missing counts records that left without a log.source or an event.category.
type missing struct{ unsourced, uncategorized int64 }

// tagRecords runs the record rules over one resource. It returns the source its records share and whether
// they disagree.
func (p *processorImpl) tagRecords(rl plog.ResourceLogs, res pcommon.Map, counts *missing) (source string, mixed bool) {
	for j := 0; j < rl.ScopeLogs().Len(); j++ {
		sl := rl.ScopeLogs().At(j)
		records := sl.LogRecords()
		for k := 0; k < records.Len(); k++ {
			lr := records.At(k)
			p.rules.record(res, sl.Scope().Attributes(), lr)
			if v, ok := lr.Attributes().Get(sourceAttr); ok && v.Type() == pcommon.ValueTypeStr && v.Str() != "" {
				switch {
				case source == "":
					source = v.Str()
				case source != v.Str():
					mixed = true
				}
			}
			if !hasValue(lr.Attributes(), sourceAttr) {
				counts.unsourced++
			}
			if !hasValue(lr.Attributes(), categoryAttr) {
				counts.uncategorized++
			}
		}
	}
	return source, mixed
}
