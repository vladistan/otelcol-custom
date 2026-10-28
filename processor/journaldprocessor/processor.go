// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package journaldprocessor

import (
	"context"
	"strconv"
	"strings"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
)

const (
	fieldPriority = "PRIORITY"
	fieldMessage  = "MESSAGE"
	attrFields    = "journald.fields"
)

// resourceFields is ordered; the tuple key and the mapped-field set both derive from it.
var resourceFields = [...]struct{ field, attr string }{
	{"_HOSTNAME", "host.name"},
	{"_MACHINE_ID", "host.id"},
	{"_PID", "process.pid"},
	{"_COMM", "process.command"},
	{"_SYSTEMD_UNIT", "service.name"},
}

type journaldProcessor struct {
	config *Config
	next   consumer.Logs
}

func (p *journaldProcessor) Start(context.Context, component.Host) error { return nil }

func (p *journaldProcessor) Shutdown(context.Context) error { return nil }

func (p *journaldProcessor) Capabilities() consumer.Capabilities {
	return consumer.Capabilities{MutatesData: true}
}

func (p *journaldProcessor) ConsumeLogs(ctx context.Context, ld plog.Logs) error {
	rls := ld.ResourceLogs()
	if p.config.SetResource && p.config.SplitResources {
		originalCount := rls.Len()
		for i := 0; i < originalCount; i++ {
			rl := rls.At(i)
			if singleTuple(rl) {
				p.processInPlace(rl)
			} else {
				p.split(rl, rls)
			}
		}
		rls.RemoveIf(func(rl plog.ResourceLogs) bool { return rl.ScopeLogs().Len() == 0 })
	} else {
		for i := 0; i < rls.Len(); i++ {
			p.processInPlace(rls.At(i))
		}
	}
	return p.next.ConsumeLogs(ctx, ld)
}

func (p *journaldProcessor) processInPlace(rl plog.ResourceLogs) {
	res := rl.Resource().Attributes()
	for j := 0; j < rl.ScopeLogs().Len(); j++ {
		recs := rl.ScopeLogs().At(j).LogRecords()
		for k := 0; k < recs.Len(); k++ {
			lr := recs.At(k)
			if lr.Body().Type() == pcommon.ValueTypeMap {
				p.processRecord(lr, res)
			}
		}
	}
}

// split moves map records into new ResourceLogs keyed by resource tuple (AD-3).
// Non-map records stay in rl with its resource unchanged (R-3).
func (p *journaldProcessor) split(rl plog.ResourceLogs, out plog.ResourceLogsSlice) {
	groups := map[string]plog.ResourceLogs{}
	scopes := map[string][]plog.ScopeLogs{}

	for j := 0; j < rl.ScopeLogs().Len(); j++ {
		sl := rl.ScopeLogs().At(j)
		recs := sl.LogRecords()
		for k := 0; k < recs.Len(); k++ {
			lr := recs.At(k)
			if lr.Body().Type() != pcommon.ValueTypeMap {
				continue
			}
			key := tupleKey(lr.Body().Map())
			group, ok := groups[key]
			if !ok {
				group = out.AppendEmpty()
				rl.Resource().CopyTo(group.Resource())
				groups[key] = group
				scopes[key] = make([]plog.ScopeLogs, rl.ScopeLogs().Len())
			}
			dst := scopes[key][j]
			if dst == (plog.ScopeLogs{}) {
				dst = group.ScopeLogs().AppendEmpty()
				sl.Scope().CopyTo(dst.Scope())
				dst.SetSchemaUrl(sl.SchemaUrl())
				scopes[key][j] = dst
			}
			moved := dst.LogRecords().AppendEmpty()
			lr.CopyTo(moved)
			p.processRecord(moved, group.Resource().Attributes())
		}
		recs.RemoveIf(func(lr plog.LogRecord) bool { return lr.Body().Type() == pcommon.ValueTypeMap })
	}
	rl.ScopeLogs().RemoveIf(func(sl plog.ScopeLogs) bool { return sl.LogRecords().Len() == 0 })
}

func (p *journaldProcessor) processRecord(lr plog.LogRecord, res pcommon.Map) {
	body := lr.Body().Map()

	if p.config.SetResource {
		for _, f := range resourceFields {
			if v, ok := body.Get(f.field); ok {
				v.CopyTo(res.PutEmpty(f.attr))
			}
		}
	}
	if p.config.MapSeverity {
		if v, ok := body.Get(fieldPriority); ok {
			if n, ok := priority(v); ok {
				num, text := severity(n)
				lr.SetSeverityNumber(num)
				lr.SetSeverityText(text)
			}
		}
	}
	if p.config.KeepFields {
		keepUnmapped(lr, body)
	}
	if p.config.BodyFromMessage {
		if v, ok := body.Get(fieldMessage); ok {
			msg := v.AsString()
			lr.Body().SetStr(msg)
		}
	}
}

func keepUnmapped(lr plog.LogRecord, body pcommon.Map) {
	var fields pcommon.Map
	body.Range(func(k string, v pcommon.Value) bool {
		if isMapped(k) {
			return true
		}
		if fields == (pcommon.Map{}) {
			fields = lr.Attributes().PutEmptyMap(attrFields)
		}
		v.CopyTo(fields.PutEmpty(k))
		return true
	})
}

func isMapped(field string) bool {
	if field == fieldPriority || field == fieldMessage {
		return true
	}
	for _, f := range resourceFields {
		if f.field == field {
			return true
		}
	}
	return false
}

// priority returns PRIORITY as 0-7; anything else reports false so severity stays unchanged.
func priority(v pcommon.Value) (int, bool) {
	var n int
	switch v.Type() {
	case pcommon.ValueTypeStr:
		parsed, err := strconv.Atoi(v.Str())
		if err != nil {
			return 0, false
		}
		n = parsed
	case pcommon.ValueTypeInt:
		n = int(v.Int())
	default:
		return 0, false
	}
	return n, n >= 0 && n <= 7
}

func severity(priority int) (plog.SeverityNumber, string) {
	switch {
	case priority <= 2:
		return plog.SeverityNumberFatal, "FATAL"
	case priority == 3:
		return plog.SeverityNumberError, "ERROR"
	case priority == 4:
		return plog.SeverityNumberWarn, "WARN"
	case priority <= 6:
		return plog.SeverityNumberInfo, "INFO"
	default:
		return plog.SeverityNumberDebug, "DEBUG"
	}
}

// singleTuple reports whether every record is a map body sharing one resource tuple.
func singleTuple(rl plog.ResourceLogs) bool {
	var first pcommon.Map
	seen := false
	for j := 0; j < rl.ScopeLogs().Len(); j++ {
		recs := rl.ScopeLogs().At(j).LogRecords()
		for k := 0; k < recs.Len(); k++ {
			b := recs.At(k).Body()
			if b.Type() != pcommon.ValueTypeMap {
				return false
			}
			if !seen {
				first, seen = b.Map(), true
				continue
			}
			if !sameTuple(first, b.Map()) {
				return false
			}
		}
	}
	return true
}

func sameTuple(a, b pcommon.Map) bool {
	for _, f := range resourceFields {
		av, aok := a.Get(f.field)
		bv, bok := b.Get(f.field)
		if aok != bok || (aok && av.AsString() != bv.AsString()) {
			return false
		}
	}
	return true
}

func tupleKey(body pcommon.Map) string {
	var b strings.Builder
	for _, f := range resourceFields {
		if v, ok := body.Get(f.field); ok {
			b.WriteByte('1')
			b.WriteString(v.AsString())
		} else {
			b.WriteByte('0')
		}
		b.WriteByte(0)
	}
	return b.String()
}
