// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package identityfillprocessor

import (
	"context"
	"strings"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
)

const (
	hostAttr              = "host.name"
	serviceAttr           = "service.name"
	eventReasonAttr       = "k8s.event.reason"
	podPhaseAttr          = "k8s.pod.phase"
	auditMethodAttr       = "gcp.audit.method.name"
	auditResourceAttr     = "gcp.audit.resource.name"
	cloudResourceIDAttr   = "cloud.resource_id"
	auditResourceIDPrefix = "cloudaudit"
)

type processorImpl struct {
	cfg  *Config
	next consumer.Logs
	tel  *telemetry
}

// identity is the host.name and service.name a record resolves to; "" means no value found.
type identity struct {
	host, service string
}

func (p *processorImpl) Start(context.Context, component.Host) error { return nil }

func (p *processorImpl) Shutdown(context.Context) error { return nil }

func (p *processorImpl) Capabilities() consumer.Capabilities {
	return consumer.Capabilities{MutatesData: true}
}

func (p *processorImpl) ConsumeLogs(ctx context.Context, ld plog.Logs) error {
	var t tally
	out := plog.NewLogs()
	for i := 0; i < ld.ResourceLogs().Len(); i++ {
		p.fillResource(ld.ResourceLogs().At(i), out, &t)
	}
	p.tel.flush(ctx, t)
	if out.LogRecordCount() == 0 {
		return nil
	}
	return p.next.ConsumeLogs(ctx, out)
}

// fillResource moves rl into out. The values live on the resource but node name and record kind
// live on each record, so a resource whose records resolve differently is split, one per identity.
func (p *processorImpl) fillResource(rl plog.ResourceLogs, out plog.Logs, t *tally) {
	res := rl.Resource().Attributes()
	fixed := identity{host: stringAttr(res, hostAttr), service: stringAttr(res, serviceAttr)}

	var ids, order []identity
	index := map[identity]int{}
	for si := 0; si < rl.ScopeLogs().Len(); si++ {
		records := rl.ScopeLogs().At(si).LogRecords()
		for ri := 0; ri < records.Len(); ri++ {
			lr := records.At(ri)
			id := p.identify(res, lr, fixed)
			if p.fillBody(res, lr) {
				t.body++
			}
			if fixed.host == "" && id.host != "" {
				t.host++
			}
			if fixed.service == "" && id.service != "" {
				t.service++
			}
			ids = append(ids, id)
			if _, seen := index[id]; !seen {
				index[id] = len(order)
				order = append(order, id)
			}
		}
	}

	if len(order) > 1 {
		split(rl, order, index, ids, out)
		return
	}
	if len(order) == 1 {
		setIdentity(res, order[0])
	}
	rl.MoveTo(out.ResourceLogs().AppendEmpty())
}

func split(rl plog.ResourceLogs, order []identity, index map[identity]int, ids []identity, out plog.Logs) {
	groups := make([]plog.ResourceLogs, len(order))
	for g, id := range order {
		groups[g] = out.ResourceLogs().AppendEmpty()
		rl.Resource().CopyTo(groups[g].Resource())
		groups[g].SetSchemaUrl(rl.SchemaUrl())
		setIdentity(groups[g].Resource().Attributes(), id)
	}

	scopes := map[[2]int]plog.ScopeLogs{}
	n := 0
	for si := 0; si < rl.ScopeLogs().Len(); si++ {
		sl := rl.ScopeLogs().At(si)
		for ri := 0; ri < sl.LogRecords().Len(); ri++ {
			key := [2]int{index[ids[n]], si}
			n++
			dst, ok := scopes[key]
			if !ok {
				dst = groups[key[0]].ScopeLogs().AppendEmpty()
				sl.Scope().CopyTo(dst.Scope())
				dst.SetSchemaUrl(sl.SchemaUrl())
				scopes[key] = dst
			}
			sl.LogRecords().At(ri).CopyTo(dst.LogRecords().AppendEmpty())
		}
	}
}

// identify keeps the values the resource already has and resolves the others for one record.
func (p *processorImpl) identify(res pcommon.Map, lr plog.LogRecord, fixed identity) identity {
	id := fixed
	if id.host == "" && !p.cfg.HostName.Disabled {
		for _, source := range p.cfg.HostName.Sources {
			if id.host = lookup(res, lr, source); id.host != "" {
				break
			}
		}
	}
	if id.service == "" && !p.cfg.ServiceName.Disabled {
		id.service = p.resolveService(res, lr)
	}
	return id
}

func (p *processorImpl) resolveService(res pcommon.Map, lr plog.LogRecord) string {
	names := p.cfg.ServiceName
	switch {
	case lookup(res, lr, eventReasonAttr) != "":
		return names.K8sEvent
	case lookup(res, lr, podPhaseAttr) != "":
		return names.K8sPodStatus
	case isAudit(res, lr):
		return names.GCPAudit
	}
	return ""
}

func isAudit(res pcommon.Map, lr plog.LogRecord) bool {
	return strings.HasPrefix(lookup(res, lr, cloudResourceIDAttr), auditResourceIDPrefix) ||
		lookup(res, lr, auditMethodAttr) != ""
}

// fillBody sets an empty audit body to "<method> <resource>" and reports whether it did.
func (p *processorImpl) fillBody(res pcommon.Map, lr plog.LogRecord) bool {
	if p.cfg.AuditBody.Disabled || !isAudit(res, lr) || !isBodyEmpty(lr.Body()) {
		return false
	}
	var parts []string
	for _, key := range []string{auditMethodAttr, auditResourceAttr} {
		if v := lookup(res, lr, key); v != "" {
			parts = append(parts, v)
		}
	}
	if len(parts) == 0 {
		return false
	}
	lr.Body().SetStr(strings.Join(parts, " "))
	return true
}

func isBodyEmpty(v pcommon.Value) bool {
	return v.Type() == pcommon.ValueTypeEmpty || (v.Type() == pcommon.ValueTypeStr && v.Str() == "")
}

// setIdentity writes the resolved values; identify already carried over what the resource had.
func setIdentity(attrs pcommon.Map, id identity) {
	if id.host != "" {
		attrs.PutStr(hostAttr, id.host)
	}
	if id.service != "" {
		attrs.PutStr(serviceAttr, id.service)
	}
}

// lookup reads a key from the record first, then from its resource.
func lookup(res pcommon.Map, lr plog.LogRecord, key string) string {
	if v := stringAttr(lr.Attributes(), key); v != "" {
		return v
	}
	return stringAttr(res, key)
}

func stringAttr(attrs pcommon.Map, key string) string {
	if v, ok := attrs.Get(key); ok {
		return v.AsString()
	}
	return ""
}
