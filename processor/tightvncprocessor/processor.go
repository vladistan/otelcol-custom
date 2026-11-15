// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package tightvncprocessor

import (
	"context"
	"strings"
	"time"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
)

const (
	byteOrderMark = "\xef\xbb\xbf"
	parseAttr     = "tightvnc.parse"
	parseFailed   = "failed"
)

type processorImpl struct {
	prefixes []string
	loc      *time.Location
	next     consumer.Logs
	tel      *telemetry
}

func (p *processorImpl) Start(context.Context, component.Host) error { return nil }

func (p *processorImpl) Shutdown(context.Context) error { return nil }

func (p *processorImpl) Capabilities() consumer.Capabilities {
	return consumer.Capabilities{MutatesData: true}
}

func (p *processorImpl) ConsumeLogs(ctx context.Context, ld plog.Logs) error {
	var t tally
	ld.ResourceLogs().RemoveIf(func(rl plog.ResourceLogs) bool {
		rl.ScopeLogs().RemoveIf(func(sl plog.ScopeLogs) bool {
			sl.LogRecords().RemoveIf(func(lr plog.LogRecord) bool { return p.process(lr, &t) })
			return sl.LogRecords().Len() == 0
		})
		return rl.ScopeLogs().Len() == 0
	})
	p.tel.flush(ctx, t)
	if ld.LogRecordCount() == 0 {
		return nil
	}
	return p.next.ConsumeLogs(ctx, ld)
}

// process reports whether the record is noise to drop. A line it cannot parse is kept, tagged
// and counted: an unseen line shape may be a real event, and a drop would hide it.
func (p *processorImpl) process(lr plog.LogRecord, t *tally) (drop bool) {
	if lr.Body().Type() != pcommon.ValueTypeStr {
		lr.Attributes().PutStr(parseAttr, parseFailed)
		t.unparsed++
		return false
	}
	body, hadMark := strings.CutPrefix(lr.Body().Str(), byteOrderMark)
	if hadMark {
		lr.Body().SetStr(body)
	}
	ln, ok := parseLine(body)
	if !ok {
		lr.Attributes().PutStr(parseAttr, parseFailed)
		t.unparsed++
		return false
	}
	if ln.marker == "" && p.isNoise(ln.message) {
		t.dropped++
		return true
	}
	p.fill(lr, ln)
	t.kept++
	return false
}

// isNoise keeps error text even under a noise prefix: a debug line that reports a failure matters.
func (p *processorImpl) isNoise(msg string) bool {
	for _, prefix := range p.prefixes {
		if strings.HasPrefix(msg, prefix) {
			return !hasErrorWord(msg)
		}
	}
	return false
}

func (p *processorImpl) fill(lr plog.LogRecord, ln line) {
	lr.Body().SetStr(ln.message)
	if when, ok := ln.logTime(p.loc); ok {
		lr.SetTimestamp(pcommon.NewTimestampFromTime(when))
	} else {
		lr.SetTimestamp(lr.ObservedTimestamp())
	}
	number, text := severityOf(ln.message)
	lr.SetSeverityNumber(number)
	lr.SetSeverityText(text)

	attrs := lr.Attributes()
	attrs.PutStr("tightvnc.marker", ln.marker)
	attrs.PutInt("tightvnc.pid", ln.pid)
	attrs.PutInt("tightvnc.tid", ln.tid)
	attrs.PutStr("tightvnc.event", eventOf(ln.message, ln.marker != ""))
	describe(attrs, ln.message)
}
