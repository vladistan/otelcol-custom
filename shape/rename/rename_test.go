// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package rename

import (
	"testing"

	"go.opentelemetry.io/collector/pdata/pcommon"
)

func mapOf(kv map[string]any) pcommon.Map {
	m := pcommon.NewMap()
	if err := m.FromRaw(kv); err != nil {
		panic(err)
	}
	return m
}

func mustStr(t *testing.T, m pcommon.Map, key, want string) {
	t.Helper()
	v, ok := m.Get(key)
	if !ok {
		t.Fatalf("%s missing; have %v", key, m.AsRaw())
	}
	if v.Type() != pcommon.ValueTypeStr || v.Str() != want {
		t.Fatalf("%s = %v (%s), want string %q", key, v.AsRaw(), v.Type(), want)
	}
}

func mustInt(t *testing.T, m pcommon.Map, key string, want int64) {
	t.Helper()
	v, ok := m.Get(key)
	if !ok {
		t.Fatalf("%s missing; have %v", key, m.AsRaw())
	}
	if v.Type() != pcommon.ValueTypeInt || v.Int() != want {
		t.Fatalf("%s = %v (%s), want int %d", key, v.AsRaw(), v.Type(), want)
	}
}

func mustAbsent(t *testing.T, m pcommon.Map, keys ...string) {
	t.Helper()
	for _, k := range keys {
		if _, ok := m.Get(k); ok {
			t.Fatalf("%s still present: %v", k, m.AsRaw())
		}
	}
}

func TestApply_EachRenameOfTheTable_MovesTheValueInPlace(t *testing.T) {
	for _, c := range []struct{ from, to string }{
		{"file.size_final", "file.size"},
		{"common_name", "user.name"},
		{"interface", "network.interface.name"},
		{"client.mac", "client.mac.address"},
		{"client.ip", "client.address"},
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
		{"code.function", "code.function.name"},
		{"k8s.pod.phase", "k8s.pod.status.phase"},
	} {
		t.Run(c.from, func(t *testing.T) {
			m := mapOf(map[string]any{c.from: "v", "keep": "k"})
			Apply(m)
			mustAbsent(t, m, c.from)
			mustStr(t, m, c.to, "v")
			mustStr(t, m, "keep", "k")
		})
	}
}

func TestApply_RealAddress_SplitsIntoClientAddressAndIntegerPort(t *testing.T) {
	m := mapOf(map[string]any{"real_address": "198.51.100.60:51234"})
	Apply(m)
	mustAbsent(t, m, "real_address")
	mustStr(t, m, "client.address", "198.51.100.60")
	mustInt(t, m, "client.port", 51234)
}

func TestApply_RealAddressWithoutPort_GivesOnlyTheAddress(t *testing.T) {
	m := mapOf(map[string]any{"real_address": "198.51.100.60"})
	Apply(m)
	mustStr(t, m, "client.address", "198.51.100.60")
	mustAbsent(t, m, "client.port", "real_address")
}

func TestApply_RealAddressIPv6_KeepsTheBrackets(t *testing.T) {
	m := mapOf(map[string]any{"real_address": "[2001:db8::1]:443"})
	Apply(m)
	mustStr(t, m, "client.address", "2001:db8::1")
	mustInt(t, m, "client.port", 443)
}

func TestApply_WinlogComputerName_IsDroppedAndHostNameKept(t *testing.T) {
	m := mapOf(map[string]any{"winlog.computer_name": "WIN-HOST-A", "host.name": "win-host-a"})
	Apply(m)
	mustAbsent(t, m, "winlog.computer_name")
	mustStr(t, m, "host.name", "win-host-a")
}

func TestApply_DuplicateTarget_KeepsTheNewKeyAndDropsTheOld(t *testing.T) {
	m := mapOf(map[string]any{"k8s.pod.phase": "Failed", "k8s.pod.status.phase": "Failed"})
	Apply(m)
	mustAbsent(t, m, "k8s.pod.phase")
	mustStr(t, m, "k8s.pod.status.phase", "Failed")

	m = mapOf(map[string]any{"client.ip": "1.1.1.1", "client.address": "2.2.2.2"})
	Apply(m)
	mustAbsent(t, m, "client.ip")
	mustStr(t, m, "client.address", "2.2.2.2")
}

func TestApply_IntegerKeys_BecomeIntegers(t *testing.T) {
	m := mapOf(map[string]any{
		"thread.id": "73", "process.pid": "1", "client.port": "443",
		"source.as.number": "13335", "destination.as.number": "15169", "file.size": "28467", "code.line.number": "214",
	})
	Apply(m)
	mustInt(t, m, "thread.id", 73)
	mustInt(t, m, "process.pid", 1)
	mustInt(t, m, "client.port", 443)
	mustInt(t, m, "source.as.number", 13335)
	mustInt(t, m, "destination.as.number", 15169)
	mustInt(t, m, "file.size", 28467)
	mustInt(t, m, "code.line.number", 214)
}

func TestApply_RenamedKeyAlsoBecomesAnInteger(t *testing.T) {
	m := mapOf(map[string]any{"winlog.process.pid": "4120", "flow.src_as": "64512", "file.size_final": "9"})
	Apply(m)
	mustInt(t, m, "process.pid", 4120)
	mustInt(t, m, "source.as.number", 64512)
	mustInt(t, m, "file.size", 9)
}

func TestApply_IntegerKeyThatIsNotANumber_StaysAString(t *testing.T) {
	m := mapOf(map[string]any{"process.pid": "n/a"})
	Apply(m)
	mustStr(t, m, "process.pid", "n/a")
}

func TestApply_AlreadyInteger_IsLeftAlone(t *testing.T) {
	m := mapOf(map[string]any{"process.pid": int64(7)})
	Apply(m)
	mustInt(t, m, "process.pid", 7)
}

func TestApply_CodeFilepath_SplitsFileAndLine(t *testing.T) {
	m := mapOf(map[string]any{"code.filepath": "cidrallocator.go:277"})
	Apply(m)
	mustAbsent(t, m, "code.filepath")
	mustStr(t, m, "code.file.path", "cidrallocator.go")
	mustInt(t, m, "code.line.number", 277)
}

func TestApply_CodeFilepathWithoutLine_MovesTheValue(t *testing.T) {
	m := mapOf(map[string]any{"code.filepath": "pkg/nri/nri.go"})
	Apply(m)
	mustStr(t, m, "code.file.path", "pkg/nri/nri.go")
	mustAbsent(t, m, "code.line.number")
}

func TestApply_CodeLineno_BecomesAnIntegerLineNumber(t *testing.T) {
	m := mapOf(map[string]any{"code.lineno": "88"})
	Apply(m)
	mustAbsent(t, m, "code.lineno")
	mustInt(t, m, "code.line.number", 88)
}

func TestApply_RPCServiceIsFoldedIntoTheMethod(t *testing.T) {
	m := mapOf(map[string]any{"rpc.service": "google.cloud.Svc", "rpc.method": "Get"})
	Apply(m)
	mustAbsent(t, m, "rpc.service")
	mustStr(t, m, "rpc.method", "google.cloud.Svc/Get")

	m = mapOf(map[string]any{"rpc.service": "google.cloud.Svc"})
	Apply(m)
	mustAbsent(t, m, "rpc.service")
	mustAbsent(t, m, "rpc.method")
}

func TestApply_WritesNoDeprecatedName(t *testing.T) {
	all := map[string]any{}
	for _, k := range Deprecated() {
		all[k] = "x"
	}
	m := mapOf(all)
	Apply(m)
	for _, k := range Deprecated() {
		if k == "event.name" {
			continue
		}
		mustAbsent(t, m, k)
	}
}

func TestDeprecated_ListsTheNamesOfFR989(t *testing.T) {
	have := map[string]bool{}
	for _, k := range Deprecated() {
		have[k] = true
	}
	for _, k := range []string{
		"deployment.environment", "rpc.system", "rpc.service", "rpc.response.status_code",
		"event.name", "code.filepath", "code.lineno", "code.function",
	} {
		if !have[k] {
			t.Errorf("%s missing from Deprecated()", k)
		}
	}
}

func TestApplyWith_ExtraRulesExtendTheDefaults(t *testing.T) {
	m := mapOf(map[string]any{"my.old": "1", "common_name": "tyler"})
	ApplyWith(m, []Rule{{From: "my.old", To: "x.custom.new"}})
	mustStr(t, m, "x.custom.new", "1")
	mustStr(t, m, "user.name", "tyler")
}

func TestFileLine_SplitsAFileAndALineNumber(t *testing.T) {
	for _, c := range []struct {
		in   string
		file string
		line int64
		ok   bool
	}{
		{"cidrallocator.go:277", "cidrallocator.go", 277, true},
		{"internal/retry_sender.go:133", "internal/retry_sender.go", 133, true},
		{"pkg/nri/nri.go", "pkg/nri/nri.go", 0, false},
		{`C:\dir\file.go`, `C:\dir\file.go`, 0, false},
		{"file.go:abc", "file.go:abc", 0, false},
		{"", "", 0, false},
	} {
		file, line, ok := FileLine(c.in)
		if file != c.file || line != c.line || ok != c.ok {
			t.Errorf("FileLine(%q) = %q, %d, %v; want %q, %d, %v", c.in, file, line, ok, c.file, c.line, c.ok)
		}
	}
}

func TestApply_KeysWithoutARule_AreUntouched(t *testing.T) {
	m := mapOf(map[string]any{"log.source": "lb", "flow.type": "t", "http.request.method": "GET", "Namespace": "ns"})
	Apply(m)
	mustStr(t, m, "log.source", "lb")
	mustStr(t, m, "flow.type", "t")
	mustStr(t, m, "http.request.method", "GET")
	mustStr(t, m, "Namespace", "ns")
}

func TestApplyRules_AppliesOnlyTheGivenRules(t *testing.T) {
	m := mapOf(map[string]any{"flow.src_as": int64(65000), "flow.dst_as": "65001", "client.ip": "10.0.0.1", "flow.type": "netflow"})

	ApplyRules(m, []Rule{{From: "flow.src_as", To: "source.as.number"}, {From: "flow.dst_as", To: "destination.as.number"}})

	mustInt(t, m, "source.as.number", 65000)
	mustInt(t, m, "destination.as.number", 65001)
	mustAbsent(t, m, "flow.src_as", "flow.dst_as")
	mustStr(t, m, "client.ip", "10.0.0.1")
	mustStr(t, m, "flow.type", "netflow")
}
