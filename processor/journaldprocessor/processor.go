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
	"go.opentelemetry.io/otel/metric"
)

const (
	fieldPriority = "PRIORITY"
	fieldMessage  = "MESSAGE"
	attrFields    = "journald.fields"
)

type journaldProcessor struct {
	config *Config
	next   consumer.Logs

	// resFields are the journal fields that become resource attributes; logFields become
	// log attributes. Both follow map_extended.
	resFields []fieldMap
	logFields []fieldMap
	mapped    map[string]bool
	resources metric.Int64Histogram
}

func newJournaldProcessor(cfg *Config, next consumer.Logs, resources metric.Int64Histogram) *journaldProcessor {
	p := &journaldProcessor{
		config:    cfg,
		next:      next,
		resFields: append([]fieldMap{}, resourceFields...),
		mapped:    map[string]bool{fieldPriority: true, fieldMessage: true},
		resources: resources,
	}
	if cfg.MapExtended {
		p.resFields = append(p.resFields, extendedResourceFields...)
		p.logFields = extendedLogFields
	}
	for _, f := range append(append([]fieldMap{}, p.resFields...), p.logFields...) {
		p.mapped[f.field] = true
	}
	return p
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
			owned := p.ownedAttrs(rl.Resource().Attributes())
			if p.allOwned(owned) || p.singleTuple(rl, owned) {
				p.processInPlace(rl, owned)
			} else {
				p.split(rl, rls, owned)
			}
		}
		rls.RemoveIf(func(rl plog.ResourceLogs) bool { return rl.ScopeLogs().Len() == 0 })
	} else {
		for i := 0; i < rls.Len(); i++ {
			rl := rls.At(i)
			p.processInPlace(rl, p.ownedAttrs(rl.Resource().Attributes()))
		}
	}
	p.resources.Record(ctx, int64(rls.Len()))
	return p.next.ConsumeLogs(ctx, ld)
}

func (p *journaldProcessor) processInPlace(rl plog.ResourceLogs, owned map[string]bool) {
	res := rl.Resource().Attributes()
	for j := 0; j < rl.ScopeLogs().Len(); j++ {
		recs := rl.ScopeLogs().At(j).LogRecords()
		for k := 0; k < recs.Len(); k++ {
			lr := recs.At(k)
			if lr.Body().Type() == pcommon.ValueTypeMap {
				p.processRecord(lr, res, owned)
			}
		}
	}
}

// hasValue is false for a missing attribute and for an empty string.
func hasValue(res pcommon.Map, key string) bool {
	v, ok := res.Get(key)
	return ok && (v.Type() != pcommon.ValueTypeStr || v.Str() != "")
}

// ownedAttrs lists the mapped resource attributes the journal must not replace: those on the
// resource before any record was read, an earlier stage having set them on purpose, such as
// service.name from a container name. An empty string counts as missing, so a failed detector
// does not block the journal. A resource that carries an attribute of skip_when_resource_has
// also owns process.pid, process.command, service.name and container.id.
func (p *journaldProcessor) ownedAttrs(res pcommon.Map) map[string]bool {
	owned := map[string]bool{}
	for _, f := range p.resFields {
		if hasValue(res, f.attr) {
			owned[f.attr] = true
		}
	}
	for _, key := range p.config.SkipWhenResourceHas {
		if hasValue(res, key) {
			for _, attr := range guardedAttrs {
				owned[attr] = true
			}
			break
		}
	}
	return owned
}

func (p *journaldProcessor) allOwned(owned map[string]bool) bool {
	for _, f := range p.resFields {
		if !owned[f.attr] {
			return false
		}
	}
	return true
}

// split moves map records into new ResourceLogs keyed by the resource tuple of
// the attributes the resource does not own. Non-map records stay in rl with its
// resource unchanged.
func (p *journaldProcessor) split(rl plog.ResourceLogs, out plog.ResourceLogsSlice, owned map[string]bool) {
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
			key := p.tupleKey(lr.Body().Map(), owned)
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
			p.processRecord(moved, group.Resource().Attributes(), owned)
		}
		recs.RemoveIf(func(lr plog.LogRecord) bool { return lr.Body().Type() == pcommon.ValueTypeMap })
	}
	rl.ScopeLogs().RemoveIf(func(sl plog.ScopeLogs) bool { return sl.LogRecords().Len() == 0 })
}

// processRecord maps one record. The order matters: fields are read from the map body first, then
// keep_fields copies the unmapped ones, body_from_message replaces the body, and the underscore
// drop runs last, so it changes only a map body that survives.
func (p *journaldProcessor) processRecord(lr plog.LogRecord, res pcommon.Map, owned map[string]bool) {
	body := lr.Body().Map()

	if p.config.SetResource {
		for _, f := range p.resFields {
			if owned[f.attr] {
				continue
			}
			if v, ok := body.Get(f.field); ok {
				v.CopyTo(res.PutEmpty(f.attr))
			}
		}
	}
	for _, f := range p.logFields {
		if v, ok := body.Get(f.field); ok {
			v.CopyTo(lr.Attributes().PutEmpty(f.attr))
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
	if p.config.MapExtended {
		p.moveLeftovers(lr, body)
	}
	if p.config.KeepFields {
		p.keepUnmapped(lr, body)
	}
	if p.config.BodyFromMessage {
		if v, ok := body.Get(fieldMessage); ok {
			msg := v.AsString()
			lr.Body().SetStr(msg)
		}
	}
	if p.config.DropUnmappedUnderscore {
		dropUnderscoreKeys(lr)
	}
}

// moveLeftovers runs under map_extended once every mapping has read the body. It drops the four
// internal keys, moves each remaining unmapped key that starts with _ into journald.unknown_fields
// (set only when there is one), and removes the keys the attributes already carry.
func (p *journaldProcessor) moveLeftovers(lr plog.LogRecord, body pcommon.Map) {
	var unknown pcommon.Map
	body.RemoveIf(func(key string, v pcommon.Value) bool {
		if internalFields[key] {
			return true
		}
		if !strings.HasPrefix(key, "_") || p.mapped[key] {
			return false
		}
		if unknown == (pcommon.Map{}) {
			unknown = lr.Attributes().PutEmptyMap(attrUnknownFields)
		}
		v.CopyTo(unknown.PutEmpty(key))
		return true
	})
	for _, key := range removedFromBody {
		body.Remove(key)
	}
}

// dropUnderscoreKeys strips the keys that start with _ from a map body that has no MESSAGE.
func dropUnderscoreKeys(lr plog.LogRecord) {
	if lr.Body().Type() != pcommon.ValueTypeMap {
		return
	}
	body := lr.Body().Map()
	if _, hasMessage := body.Get(fieldMessage); hasMessage {
		return
	}
	body.RemoveIf(func(key string, _ pcommon.Value) bool { return strings.HasPrefix(key, "_") })
}

func (p *journaldProcessor) keepUnmapped(lr plog.LogRecord, body pcommon.Map) {
	var fields pcommon.Map
	body.Range(func(k string, v pcommon.Value) bool {
		if p.mapped[k] {
			return true
		}
		if fields == (pcommon.Map{}) {
			fields = lr.Attributes().PutEmptyMap(attrFields)
		}
		v.CopyTo(fields.PutEmpty(k))
		return true
	})
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

func severity(level int) (plog.SeverityNumber, string) {
	switch {
	case level <= 2:
		return plog.SeverityNumberFatal, "FATAL"
	case level == 3:
		return plog.SeverityNumberError, "ERROR"
	case level == 4:
		return plog.SeverityNumberWarn, "WARN"
	case level <= 6:
		return plog.SeverityNumberInfo, "INFO"
	default:
		return plog.SeverityNumberDebug, "DEBUG"
	}
}

// singleTuple reports whether every record is a map body sharing one resource
// tuple; fields of attributes the resource owns are not compared.
func (p *journaldProcessor) singleTuple(rl plog.ResourceLogs, owned map[string]bool) bool {
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
			if !p.sameTuple(first, b.Map(), owned) {
				return false
			}
		}
	}
	return true
}

func (p *journaldProcessor) sameTuple(a, b pcommon.Map, owned map[string]bool) bool {
	for _, f := range p.resFields {
		if owned[f.attr] {
			continue
		}
		av, aok := a.Get(f.field)
		bv, bok := b.Get(f.field)
		if aok != bok || (aok && av.AsString() != bv.AsString()) {
			return false
		}
	}
	return true
}

func (p *journaldProcessor) tupleKey(body pcommon.Map, owned map[string]bool) string {
	var key strings.Builder
	for _, f := range p.resFields {
		if owned[f.attr] {
			continue
		}
		if v, ok := body.Get(f.field); ok {
			key.WriteByte('1')
			key.WriteString(v.AsString())
		} else {
			key.WriteByte('0')
		}
		key.WriteByte(0)
	}
	return key.String()
}
