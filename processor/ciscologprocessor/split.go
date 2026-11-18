// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package ciscologprocessor

import (
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
)

const (
	hostKey    = "host.name"
	serviceKey = "service.name" // pragma: allowlist secret
	peerKey    = "net.peer.name"
)

// hostService is the part of a resource that processing one record may change.
type hostService struct {
	host, service       string
	hasHost, hasService bool
}

// processPerResource runs fn on each record and moves the record under a ResourceLogs that carries the
// host.name and service.name fn left. fn sees a small scratch map with only host.name, service.name and
// net.peer.name, so cost does not grow with resource width. Groups form inside one input ResourceLogs, in
// first-seen order; each copies the input resource and scope.
func processPerResource(in plog.Logs, fn func(plog.LogRecord, pcommon.Map)) plog.Logs {
	out := plog.NewLogs()
	scratch := pcommon.NewMap()
	for i := 0; i < in.ResourceLogs().Len(); i++ {
		rl := in.ResourceLogs().At(i)
		base := rl.Resource().Attributes()
		groups := map[hostService]*resourceGroup{}
		for j := 0; j < rl.ScopeLogs().Len(); j++ {
			sl := rl.ScopeLogs().At(j)
			for k := 0; k < sl.LogRecords().Len(); k++ {
				lr := sl.LogRecords().At(k)
				loadScratch(scratch, base)
				fn(lr, scratch)
				res := readHostService(scratch)
				g := groups[res]
				if g == nil {
					g = newResourceGroup(out, rl, res)
					groups[res] = g
				}
				lr.MoveTo(g.scope(j, sl).LogRecords().AppendEmpty())
			}
		}
	}
	return out
}

func loadScratch(scratch, base pcommon.Map) {
	scratch.Clear()
	for _, key := range []string{hostKey, serviceKey, peerKey} {
		if v, ok := base.Get(key); ok {
			v.CopyTo(scratch.PutEmpty(key))
		}
	}
}

func readHostService(m pcommon.Map) hostService {
	var r hostService
	if v, ok := m.Get(hostKey); ok {
		r.host, r.hasHost = v.AsString(), true
	}
	if v, ok := m.Get(serviceKey); ok {
		r.service, r.hasService = v.AsString(), true
	}
	return r
}

type resourceGroup struct {
	rl     plog.ResourceLogs
	scopes map[int]plog.ScopeLogs
}

func newResourceGroup(out plog.Logs, from plog.ResourceLogs, res hostService) *resourceGroup {
	rl := out.ResourceLogs().AppendEmpty()
	rl.SetSchemaUrl(from.SchemaUrl())
	from.Resource().CopyTo(rl.Resource())
	attrs := rl.Resource().Attributes()
	setString(attrs, hostKey, res.host, res.hasHost)
	setString(attrs, serviceKey, res.service, res.hasService)
	return &resourceGroup{rl: rl, scopes: map[int]plog.ScopeLogs{}}
}

// setString makes the attribute hold the value, or removes it; an attribute that already reads the same is left as it was.
func setString(attrs pcommon.Map, key, value string, present bool) {
	cur, has := attrs.Get(key)
	switch {
	case !present:
		attrs.Remove(key)
	case !has || cur.AsString() != value:
		attrs.PutStr(key, value)
	}
}

// scope returns the scope of the group that copies the scope at index i of the input.
func (g *resourceGroup) scope(i int, from plog.ScopeLogs) plog.ScopeLogs {
	if sl, ok := g.scopes[i]; ok {
		return sl
	}
	sl := g.rl.ScopeLogs().AppendEmpty()
	from.Scope().CopyTo(sl.Scope())
	sl.SetSchemaUrl(from.SchemaUrl())
	g.scopes[i] = sl
	return sl
}
