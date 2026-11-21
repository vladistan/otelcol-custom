// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package winlogprocessor

import (
	"strings"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
)

const (
	providerNameKey = "winlog.provider_name"
	serviceNameKey  = "service.name"
	// windowsProviderPrefix is removed from a provider name to make the service name.
	windowsProviderPrefix = "Microsoft-Windows-"
)

// providerServiceName turns a provider name into a service name: the
// Microsoft-Windows- prefix is removed, a name without it is kept, and a name
// that is only the prefix is kept whole.
func providerServiceName(provider string) string {
	if name := strings.TrimPrefix(provider, windowsProviderPrefix); name != "" {
		return name
	}
	return provider
}

// resourceGroup is the output resource for one service name taken from one
// input resource, with one scope per input scope.
type resourceGroup struct {
	resource plog.ResourceLogs
	scopes   map[int]plog.ScopeLogs
}

func (g *resourceGroup) scope(index int, from plog.ScopeLogs) plog.ScopeLogs {
	if sl, ok := g.scopes[index]; ok {
		return sl
	}
	sl := g.resource.ScopeLogs().AppendEmpty()
	from.Scope().CopyTo(sl.Scope())
	sl.SetSchemaUrl(from.SchemaUrl())
	g.scopes[index] = sl
	return sl
}

// noProvider is the group of a shaped event that names no provider. Its
// resource loses service.name, so the fallback does not stand in for a subsystem.
const noProvider = "\x00"

// splitByProvider returns the logs with one resource per service name. A
// record takes the service name of its own winlog.provider_name. A shaped event
// with no provider gets a copy of its resource without service.name; an
// unshaped record keeps a plain copy. Resources appear in order of first use
// and records keep their order. The input records are moved: do not reuse in.
func splitByProvider(in plog.Logs) plog.Logs {
	out := plog.NewLogs()
	for i := 0; i < in.ResourceLogs().Len(); i++ {
		source := in.ResourceLogs().At(i)
		groups := map[string]*resourceGroup{}
		for s := 0; s < source.ScopeLogs().Len(); s++ {
			scope := source.ScopeLogs().At(s)
			for r := 0; r < scope.LogRecords().Len(); r++ {
				record := scope.LogRecords().At(r)
				name := recordServiceName(record)
				group, ok := groups[name]
				if !ok {
					group = newResourceGroup(out, source, name)
					groups[name] = group
				}
				record.MoveTo(group.scope(s, scope).LogRecords().AppendEmpty())
			}
		}
	}
	return out
}

func newResourceGroup(out plog.Logs, source plog.ResourceLogs, serviceName string) *resourceGroup {
	rl := out.ResourceLogs().AppendEmpty()
	source.Resource().CopyTo(rl.Resource())
	rl.SetSchemaUrl(source.SchemaUrl())
	switch serviceName {
	case "":
	case noProvider:
		rl.Resource().Attributes().Remove(serviceNameKey)
	default:
		rl.Resource().Attributes().PutStr(serviceNameKey, serviceName)
	}
	return &resourceGroup{resource: rl, scopes: map[int]plog.ScopeLogs{}}
}

// recordServiceName is the service name of a shaped record, noProvider for a
// shaped event with no provider name, and "" for a record the processor left alone.
func recordServiceName(record plog.LogRecord) string {
	value, ok := record.Attributes().Get(providerNameKey)
	if ok && value.Type() == pcommon.ValueTypeStr && value.Str() != "" {
		return providerServiceName(value.Str())
	}
	if source, ok := record.Attributes().Get("log.source"); ok && strings.HasPrefix(source.AsString(), "winlog:") {
		return noProvider
	}
	return ""
}
