// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package winlogprocessor

import (
	"context"
	"time"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.uber.org/zap"
)

// summaryCheckEvery is how often the background loop looks for a full window.
const summaryCheckEvery = time.Minute

// winlogProcessor counts each event log record, drops the ones a rule matches
// and shapes and forwards the rest. With the drop summary on, it also emits a
// summary of the drops once an hour.
type winlogProcessor struct {
	next             consumer.Logs
	rules            ruleSet
	tel              *telemetry
	maxOriginalBytes int
	logger           *zap.Logger

	summary *dropSummary // nil when the summary is off
	stop    chan struct{}
	done    chan struct{}
}

func (p *winlogProcessor) Start(context.Context, component.Host) error {
	if p.summary == nil {
		return nil
	}
	p.stop = make(chan struct{})
	p.done = make(chan struct{})
	go p.summaryLoop()
	return nil
}

func (p *winlogProcessor) Shutdown(ctx context.Context) error {
	if p.stop != nil {
		close(p.stop)
		<-p.done
		p.stop = nil
	}
	return p.flushSummary(ctx, true)
}

func (p *winlogProcessor) summaryLoop() {
	defer close(p.done)
	ticker := time.NewTicker(summaryCheckEvery)
	defer ticker.Stop()
	for {
		select {
		case <-p.stop:
			return
		case <-ticker.C:
			if err := p.emitDue(context.Background()); err != nil {
				p.logger.Warn("could not emit the drop summary", zap.Error(err))
			}
		}
	}
}

// emitDue emits the drop summary when its window is an hour old.
func (p *winlogProcessor) emitDue(ctx context.Context) error {
	return p.flushSummary(ctx, false)
}

func (p *winlogProcessor) flushSummary(ctx context.Context, force bool) error {
	if p.summary == nil {
		return nil
	}
	logs, ok := p.summary.take(force)
	if !ok {
		return nil
	}
	return p.next.ConsumeLogs(ctx, logs)
}

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
		if p.summary != nil {
			p.summary.add(&p.rules.rules[idx], &fields)
		}
		return true
	}
	p.shape(record, &fields)
	return false
}
