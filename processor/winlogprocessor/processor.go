// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package winlogprocessor

import (
	"context"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/plog"
)

// winlogProcessor counts each event log record, drops the ones a rule matches
// and shapes and forwards the rest.
type winlogProcessor struct {
	next             consumer.Logs
	rules            ruleSet
	tel              *telemetry
	maxOriginalBytes int
}

func (p *winlogProcessor) Start(context.Context, component.Host) error { return nil }

func (p *winlogProcessor) Shutdown(context.Context) error { return nil }

func (p *winlogProcessor) Capabilities() consumer.Capabilities {
	return consumer.Capabilities{MutatesData: true}
}

func (p *winlogProcessor) ConsumeLogs(ctx context.Context, logs plog.Logs) error {
	var t tally
	logs.ResourceLogs().RemoveIf(func(rl plog.ResourceLogs) bool {
		rl.ScopeLogs().RemoveIf(func(sl plog.ScopeLogs) bool {
			sl.LogRecords().RemoveIf(func(record plog.LogRecord) bool { return p.handle(record, &t) })
			return sl.LogRecords().Len() == 0
		})
		return rl.ScopeLogs().Len() == 0
	})
	p.tel.flush(ctx, &t)
	if logs.ResourceLogs().Len() == 0 {
		return nil
	}
	return p.next.ConsumeLogs(ctx, logs)
}

// handle counts the record as read. It reports true when a rule drops the
// record; otherwise it shapes the record in place. A record whose body is not
// a map with a channel passes untouched and uncounted.
func (p *winlogProcessor) handle(record plog.LogRecord, counts *tally) (isDropped bool) {
	fields := p.rules.extract(record)
	if !fields.ok {
		return false
	}
	counts.addRead(fields.channel)
	if idx := p.rules.firstMatch(&fields); idx >= 0 {
		counts.addDrop(fields.channel, p.rules.rules[idx].name)
		return true
	}
	p.shape(record, &fields)
	return false
}
