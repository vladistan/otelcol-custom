// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

// Package rename moves attributes from our old names to the official OpenTelemetry names, in place.
package rename

import (
	"net"
	"strconv"
	"strings"

	"go.opentelemetry.io/collector/pdata/pcommon"
)

// Rule renames one attribute.
type Rule struct {
	From string
	To   string
}

// Table is the default rename table. A key that already holds the new name
// keeps its value and the old key is dropped.
var Table = []Rule{
	{"file.size_final", "file.size"},
	{"common_name", "user.name"},
	{"interface", "network.interface.name"},
	{"client.mac", "client.mac.address"},
	{"client.ip", "client.address"},
	{"k8s.pod.phase", "k8s.pod.status.phase"},
	{"k8s.pod.restarts", "k8s.container.restart_count"},
	{"otelcol.component", "otel.component.name"},
	{"otelcol.component.id", "otel.component.name"},
	{"original_message", "log.record.original"},
	{"flow.src_as", "source.as.number"},
	{"flow.dst_as", "destination.as.number"},
	{"winlog.user.identifier", "user.id"},
	{"winlog.process.pid", "process.pid"},
	{"winlog.process.thread.id", "thread.id"},
	{"aws.region", "cloud.region"},
	{"machine.type", "host.type"},
	{"host.display_name", "host.name"},
	{"gcp.project", "cloud.account.id"},
	{"deployment.environment", "deployment.environment.name"},
	{"rpc.system", "rpc.system.name"},
	{"rpc.response.status_code", "rpc.status_code"},
	{"code.lineno", "code.line.number"},
	{"code.function", "code.function.name"},
}

// IntKeys are the attributes written as integers.
var IntKeys = []string{"thread.id", "process.pid", "client.port", "source.as.number", "destination.as.number", "file.size", "code.line.number"}

// Deprecated lists the names our components stop writing.
func Deprecated() []string {
	return []string{
		"deployment.environment", "rpc.system", "rpc.service", "rpc.response.status_code",
		"event.name", "code.filepath", "code.lineno", "code.function",
	}
}

// Apply renames with the default table.
func Apply(m pcommon.Map) { ApplyWith(m, nil) }

// ApplyWith renames with the default table followed by extra rules.
func ApplyWith(m pcommon.Map, extra []Rule) {
	splitRealAddress(m)
	m.Remove("winlog.computer_name")
	splitCodeFilepath(m)
	foldRPCService(m)
	for _, r := range Table {
		move(m, r.From, r.To)
	}
	for _, r := range extra {
		move(m, r.From, r.To)
	}
	for _, k := range IntKeys {
		toInt(m, k)
	}
}

// ApplyRules renames with the given rules only, then converts the integer keys. The default table and the
// special splits are not applied, so a branch can rename the names of its own format and nothing else.
func ApplyRules(m pcommon.Map, rules []Rule) {
	for _, r := range rules {
		move(m, r.From, r.To)
	}
	for _, k := range IntKeys {
		toInt(m, k)
	}
}

func move(m pcommon.Map, from, to string) {
	v, ok := m.Get(from)
	if !ok {
		return
	}
	if _, exists := m.Get(to); !exists {
		tmp := pcommon.NewValueEmpty()
		v.CopyTo(tmp)
		tmp.CopyTo(m.PutEmpty(to))
	}
	m.Remove(from)
}

func toInt(m pcommon.Map, key string) {
	v, ok := m.Get(key)
	if !ok || v.Type() != pcommon.ValueTypeStr {
		return
	}
	if n, err := strconv.ParseInt(strings.TrimSpace(v.Str()), 10, 64); err == nil {
		v.SetInt(n)
	}
}

func splitRealAddress(m pcommon.Map) {
	v, ok := m.Get("real_address")
	if !ok {
		return
	}
	addr := v.Str()
	m.Remove("real_address")
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		host, port = addr, ""
	}
	if _, has := m.Get("client.address"); !has {
		m.PutStr("client.address", host)
	}
	if n, err := strconv.ParseInt(port, 10, 64); err == nil {
		if _, has := m.Get("client.port"); !has {
			m.PutInt("client.port", n)
		}
	}
}

// FileLine splits "file.go:277" into the file and the line. ok is false when there is no line number,
// and the file is then the whole value.
func FileLine(value string) (file string, line int64, ok bool) {
	i := strings.LastIndexByte(value, ':')
	if i <= 0 {
		return value, 0, false
	}
	n, err := strconv.ParseInt(value[i+1:], 10, 64)
	if err != nil {
		return value, 0, false
	}
	return value[:i], n, true
}

func splitCodeFilepath(m pcommon.Map) {
	v, ok := m.Get("code.filepath")
	if !ok {
		return
	}
	val := v.Str()
	m.Remove("code.filepath")
	file, n, hasLine := FileLine(val)
	if hasLine {
		if _, has := m.Get("code.line.number"); !has {
			if _, hasOld := m.Get("code.lineno"); !hasOld {
				m.PutInt("code.line.number", n)
			}
		}
	}
	if _, has := m.Get("code.file.path"); !has {
		m.PutStr("code.file.path", file)
	}
}

func foldRPCService(m pcommon.Map) {
	svc, ok := m.Get("rpc.service")
	if !ok {
		return
	}
	service := svc.Str()
	m.Remove("rpc.service")
	if method, has := m.Get("rpc.method"); has && service != "" {
		method.SetStr(service + "/" + method.Str())
	}
}
