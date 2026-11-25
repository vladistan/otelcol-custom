// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package journaldprocessor

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/processor/processortest"
)

func loadFixture(t *testing.T) []map[string]any {
	t.Helper()
	raw, err := os.ReadFile("testdata/records.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var records []map[string]any
	if err := json.Unmarshal(raw, &records); err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	return records
}

func defaultConfig() *Config {
	return NewFactory().CreateDefaultConfig().(*Config)
}

func appendMapRecord(t *testing.T, sl plog.ScopeLogs, body map[string]any) plog.LogRecord {
	t.Helper()
	lr := sl.LogRecords().AppendEmpty()
	if err := lr.Body().SetEmptyMap().FromRaw(body); err != nil {
		t.Fatalf("set map body: %v", err)
	}
	return lr
}

func run(t *testing.T, cfg *Config, ld plog.Logs) plog.Logs {
	t.Helper()
	sink := new(consumertest.LogsSink)
	set := processortest.NewNopSettings(component.MustNewType(typeStr))
	proc, err := NewFactory().CreateLogs(context.Background(), set, cfg, sink)
	if err != nil {
		t.Fatalf("create processor: %v", err)
	}
	if err := proc.ConsumeLogs(context.Background(), ld); err != nil {
		t.Fatalf("consume: %v", err)
	}
	if len(sink.AllLogs()) != 1 {
		t.Fatalf("sink got %d batches, want 1", len(sink.AllLogs()))
	}
	return sink.AllLogs()[0]
}

func oneRecord(t *testing.T, body map[string]any) (plog.Logs, plog.LogRecord) {
	t.Helper()
	ld := plog.NewLogs()
	sl := ld.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty()
	return ld, appendMapRecord(t, sl, body)
}

func onlyRecord(t *testing.T, ld plog.Logs) (pcommon.Map, plog.LogRecord) {
	t.Helper()
	if ld.ResourceLogs().Len() != 1 {
		t.Fatalf("got %d ResourceLogs, want 1", ld.ResourceLogs().Len())
	}
	rl := ld.ResourceLogs().At(0)
	return rl.Resource().Attributes(), rl.ScopeLogs().At(0).LogRecords().At(0)
}

func TestFieldMapping_SetsResourceAttributes(t *testing.T) {
	ld, _ := oneRecord(t, loadFixture(t)[0])

	res, _ := onlyRecord(t, run(t, defaultConfig(), ld))

	want := map[string]string{
		"host.name":       "host-a.example.com",
		"host.id":         "00000000000000000000000000000a01",
		"process.pid":     "1201",
		"process.command": "sshd",
		"service.name":    "ssh.service",
	}
	for key, value := range want {
		got, ok := res.Get(key)
		if !ok {
			t.Fatalf("resource attribute %q missing", key)
		}
		if got.AsString() != value {
			t.Errorf("%s = %q, want %q", key, got.AsString(), value)
		}
	}
}

func TestPriority_MapsEachLevelToSeverity(t *testing.T) {
	cases := []struct {
		priority string
		number   plog.SeverityNumber
		text     string
	}{
		{"0", plog.SeverityNumberFatal, "FATAL"},
		{"1", plog.SeverityNumberFatal, "FATAL"},
		{"2", plog.SeverityNumberFatal, "FATAL"},
		{"3", plog.SeverityNumberError, "ERROR"},
		{"4", plog.SeverityNumberWarn, "WARN"},
		{"5", plog.SeverityNumberInfo, "INFO"},
		{"6", plog.SeverityNumberInfo, "INFO"},
		{"7", plog.SeverityNumberDebug, "DEBUG"},
	}
	for _, tc := range cases {
		t.Run("priority_"+tc.priority, func(t *testing.T) {
			ld, _ := oneRecord(t, map[string]any{"PRIORITY": tc.priority, "MESSAGE": "m"})

			_, lr := onlyRecord(t, run(t, defaultConfig(), ld))

			if lr.SeverityNumber() != tc.number || lr.SeverityText() != tc.text {
				t.Errorf("severity = %v/%q, want %v/%q", lr.SeverityNumber(), lr.SeverityText(), tc.number, tc.text)
			}
		})
	}
}

func TestPriority_IntegerValueMapsToSeverity(t *testing.T) {
	ld, _ := oneRecord(t, map[string]any{"PRIORITY": int64(3), "MESSAGE": "m"})

	_, lr := onlyRecord(t, run(t, defaultConfig(), ld))

	if lr.SeverityNumber() != plog.SeverityNumberError {
		t.Errorf("severity = %v, want ERROR", lr.SeverityNumber())
	}
}

func TestPriority_MissingOrInvalid_LeavesSeverityUnchanged(t *testing.T) {
	cases := map[string]map[string]any{
		"missing":      {"MESSAGE": "m"},
		"non numeric":  {"PRIORITY": "abc", "MESSAGE": "m"},
		"above range":  {"PRIORITY": "8", "MESSAGE": "m"},
		"negative":     {"PRIORITY": "-1", "MESSAGE": "m"},
		"empty string": {"PRIORITY": "", "MESSAGE": "m"},
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			ld, lr := oneRecord(t, body)
			lr.SetSeverityNumber(plog.SeverityNumberTrace)
			lr.SetSeverityText("PRESET")

			_, got := onlyRecord(t, run(t, defaultConfig(), ld))

			if got.SeverityNumber() != plog.SeverityNumberTrace || got.SeverityText() != "PRESET" {
				t.Errorf("severity = %v/%q, want unchanged", got.SeverityNumber(), got.SeverityText())
			}
			if got.Body().Type() != pcommon.ValueTypeStr || got.Body().Str() != "m" {
				t.Errorf("record did not pass through with its MESSAGE body")
			}
		})
	}
}

func TestBody_WithMessage_BecomesMessageString(t *testing.T) {
	ld, _ := oneRecord(t, loadFixture(t)[0])

	_, lr := onlyRecord(t, run(t, defaultConfig(), ld))

	if lr.Body().Type() != pcommon.ValueTypeStr || lr.Body().Str() != "Accepted publickey for deploy" {
		t.Errorf("body = %v, want MESSAGE string", lr.Body().AsString())
	}
}

func TestBody_WithoutMessage_StaysMap(t *testing.T) {
	ld, _ := oneRecord(t, loadFixture(t)[3])

	_, lr := onlyRecord(t, run(t, defaultConfig(), ld))

	if lr.Body().Type() != pcommon.ValueTypeMap {
		t.Fatalf("body type = %v, want map", lr.Body().Type())
	}
	if _, ok := lr.Body().Map().Get("PRIORITY"); !ok {
		t.Error("map body lost its fields")
	}
}

func TestStringBody_PassesUntouched(t *testing.T) {
	ld := plog.NewLogs()
	rl := ld.ResourceLogs().AppendEmpty()
	rl.Resource().Attributes().PutStr("k8s.cluster.name", "c1")
	lr := rl.ScopeLogs().AppendEmpty().LogRecords().AppendEmpty()
	lr.Body().SetStr("plain text line")

	res, got := onlyRecord(t, run(t, defaultConfig(), ld))

	if got.Body().Str() != "plain text line" || got.SeverityNumber() != plog.SeverityNumberUnspecified {
		t.Errorf("string record changed: body=%q severity=%v", got.Body().Str(), got.SeverityNumber())
	}
	if res.Len() != 1 {
		t.Errorf("resource attributes changed: %v", res.AsRaw())
	}
}

func TestMissingField_AttributeNotSetOthersStillMapped(t *testing.T) {
	ld, _ := oneRecord(t, map[string]any{"_HOSTNAME": "host-a.example.com", "MESSAGE": "m"})

	res, _ := onlyRecord(t, run(t, defaultConfig(), ld))

	if _, ok := res.Get("host.id"); ok {
		t.Error("host.id set without _MACHINE_ID")
	}
	if got, ok := res.Get("host.name"); !ok || got.Str() != "host-a.example.com" {
		t.Errorf("host.name = %v, want host-a.example.com", got.AsString())
	}
}

func TestUnmappedFields_KeptUnderJournaldFields(t *testing.T) {
	ld, _ := oneRecord(t, loadFixture(t)[0])

	_, lr := onlyRecord(t, run(t, defaultConfig(), ld))

	v, ok := lr.Attributes().Get("journald.fields")
	if !ok {
		t.Fatal("journald.fields missing")
	}
	got := v.Map().AsRaw()
	want := map[string]any{"SYSLOG_IDENTIFIER": "sshd", "_TRANSPORT": "syslog"}
	if len(got) != len(want) {
		t.Fatalf("journald.fields = %v, want %v", got, want)
	}
	for key, value := range want {
		if got[key] != value {
			t.Errorf("journald.fields[%q] = %v, want %v", key, got[key], value)
		}
	}
}

func TestKeepFieldsOff_NoJournaldFields(t *testing.T) {
	cfg := defaultConfig()
	cfg.KeepFields = false
	ld, _ := oneRecord(t, loadFixture(t)[0])

	_, lr := onlyRecord(t, run(t, cfg, ld))

	if _, ok := lr.Attributes().Get("journald.fields"); ok {
		t.Error("journald.fields present with keep_fields off")
	}
}

func TestSwitchesOff_RecordIsNotChanged(t *testing.T) {
	cfg := &Config{}
	ld, _ := oneRecord(t, loadFixture(t)[0])

	res, lr := onlyRecord(t, run(t, cfg, ld))

	if res.Len() != 0 {
		t.Errorf("resource attributes set with set_resource off: %v", res.AsRaw())
	}
	if lr.SeverityNumber() != plog.SeverityNumberUnspecified {
		t.Errorf("severity set with map_severity off: %v", lr.SeverityNumber())
	}
	if lr.Body().Type() != pcommon.ValueTypeMap {
		t.Errorf("body rewritten with body_from_message off")
	}
}

func mixedBatch(t *testing.T) plog.Logs {
	t.Helper()
	fixture := loadFixture(t)
	ld := plog.NewLogs()
	rl := ld.ResourceLogs().AppendEmpty()
	rl.Resource().Attributes().PutStr("k8s.cluster.name", "c1")
	sl := rl.ScopeLogs().AppendEmpty()
	sl.Scope().SetName("journald-scope")
	appendMapRecord(t, sl, fixture[0])
	appendMapRecord(t, sl, fixture[1])
	appendMapRecord(t, sl, fixture[2])
	sl.LogRecords().AppendEmpty().Body().SetStr("plain text line")
	return ld
}

func TestSplit_MixedUnits_EachRecordUnderItsOwnResource(t *testing.T) {
	out := run(t, defaultConfig(), mixedBatch(t))

	if out.LogRecordCount() != 4 {
		t.Fatalf("record count = %d, want 4", out.LogRecordCount())
	}
	seen := map[string]string{}
	for i := 0; i < out.ResourceLogs().Len(); i++ {
		rl := out.ResourceLogs().At(i)
		unit, hasUnit := rl.Resource().Attributes().Get("service.name")
		for j := 0; j < rl.ScopeLogs().Len(); j++ {
			sl := rl.ScopeLogs().At(j)
			if sl.Scope().Name() != "journald-scope" {
				t.Errorf("scope name = %q, want journald-scope", sl.Scope().Name())
			}
			for k := 0; k < sl.LogRecords().Len(); k++ {
				body := sl.LogRecords().At(k).Body().AsString()
				if hasUnit {
					seen[body] = unit.Str()
				} else {
					seen[body] = ""
				}
			}
		}
	}
	want := map[string]string{
		"Accepted publickey for deploy": "ssh.service",
		"failed to sync pod":            "kubelet.service",
		"upstream timed out":            "nginx.service",
		"plain text line":               "",
	}
	for body, unit := range want {
		if got, ok := seen[body]; !ok || got != unit {
			t.Errorf("record %q under unit %q (found %v), want %q", body, got, ok, unit)
		}
	}
}

func TestSplit_KeepsOriginalResourceAttributes(t *testing.T) {
	out := run(t, defaultConfig(), mixedBatch(t))

	for i := 0; i < out.ResourceLogs().Len(); i++ {
		attrs := out.ResourceLogs().At(i).Resource().Attributes()
		if got, ok := attrs.Get("k8s.cluster.name"); !ok || got.Str() != "c1" {
			t.Errorf("ResourceLogs %d lost k8s.cluster.name", i)
		}
	}
}

func TestSplit_NonMapRecordStaysInOriginalGroup(t *testing.T) {
	out := run(t, defaultConfig(), mixedBatch(t))

	for i := 0; i < out.ResourceLogs().Len(); i++ {
		rl := out.ResourceLogs().At(i)
		for j := 0; j < rl.ScopeLogs().Len(); j++ {
			recs := rl.ScopeLogs().At(j).LogRecords()
			for k := 0; k < recs.Len(); k++ {
				if recs.At(k).Body().AsString() != "plain text line" {
					continue
				}
				if _, ok := rl.Resource().Attributes().Get("service.name"); ok {
					t.Error("non-map record moved under a journald unit resource")
				}
				if rl.Resource().Attributes().Len() != 1 {
					t.Errorf("original resource changed: %v", rl.Resource().Attributes().AsRaw())
				}
			}
		}
	}
}

func TestSplit_SingleTuple_MutatesInPlace(t *testing.T) {
	fixture := loadFixture(t)
	ld := plog.NewLogs()
	sl := ld.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty()
	appendMapRecord(t, sl, fixture[0])
	appendMapRecord(t, sl, fixture[3])

	out := run(t, defaultConfig(), ld)

	if out.ResourceLogs().Len() != 1 {
		t.Fatalf("got %d ResourceLogs, want 1", out.ResourceLogs().Len())
	}
	if got, _ := out.ResourceLogs().At(0).Resource().Attributes().Get("service.name"); got.Str() != "ssh.service" {
		t.Errorf("service.name = %q, want ssh.service", got.Str())
	}
}

func TestSplit_Off_KeepsOneResourceLogs(t *testing.T) {
	cfg := defaultConfig()
	cfg.SplitResources = false
	out := run(t, cfg, mixedBatch(t))

	if out.ResourceLogs().Len() != 1 {
		t.Errorf("got %d ResourceLogs with split_resources off, want 1", out.ResourceLogs().Len())
	}
}

// journalBody is the sshd record of the fixture: every mapped field is present.
func journalBody(t *testing.T) map[string]any {
	t.Helper()
	return loadFixture(t)[0]
}

func resourceWith(ld plog.Logs, attrs map[string]string) {
	for k, v := range attrs {
		ld.ResourceLogs().At(0).Resource().Attributes().PutStr(k, v)
	}
}

func assertAttr(t *testing.T, res pcommon.Map, key, want string) {
	t.Helper()
	got, ok := res.Get(key)
	if !ok {
		t.Errorf("resource attribute %q missing, want %q", key, want)
		return
	}
	if got.AsString() != want {
		t.Errorf("%s = %q, want %q", key, got.AsString(), want)
	}
}

// A record that a shaping step already rewrote keeps its journal fields in the map, so the
// resource and process attributes still come from it and the body becomes the shaped text.
func TestShapedMap_ResourceAndProcessFieldsStillComeFromTheMap(t *testing.T) {
	body := journalBody(t)
	delete(body, "PRIORITY")
	body["MESSAGE"] = "Accepted publickey for deploy"
	ld, _ := oneRecord(t, body)

	res, rec := onlyRecord(t, run(t, defaultConfig(), ld))

	assertAttr(t, res, "host.name", "host-a.example.com")
	assertAttr(t, res, "host.id", "00000000000000000000000000000a01")
	assertAttr(t, res, "process.pid", "1201")
	assertAttr(t, res, "process.command", "sshd")
	if rec.Body().Str() != "Accepted publickey for deploy" {
		t.Errorf("body = %v", rec.Body().AsRaw())
	}
}

func TestNoOverwrite_ServiceNameFromTheEdgeIsKept(t *testing.T) {
	body := journalBody(t)
	body["_SYSTEMD_UNIT"] = "docker.service"
	ld, _ := oneRecord(t, body)
	resourceWith(ld, map[string]string{"service.name": "nginx-proxy"})

	res, _ := onlyRecord(t, run(t, defaultConfig(), ld))

	assertAttr(t, res, "service.name", "nginx-proxy")
}

func TestNoOverwrite_EveryMappedAttributeAlreadyOnTheResourceIsKept(t *testing.T) {
	preset := map[string]string{
		"host.name":       "edge-host",
		"host.id":         "edge-id",
		"process.pid":     "77",
		"process.command": "app",
		"service.name":    "app-service",
	}
	ld, _ := oneRecord(t, journalBody(t))
	resourceWith(ld, preset)

	res, _ := onlyRecord(t, run(t, defaultConfig(), ld))

	for key, want := range preset {
		assertAttr(t, res, key, want)
	}
}

func TestNoOverwrite_OnlyTheMissingAttributesAreFilledFromTheJournal(t *testing.T) {
	ld, _ := oneRecord(t, journalBody(t))
	resourceWith(ld, map[string]string{"service.name": "edge-service"})

	res, _ := onlyRecord(t, run(t, defaultConfig(), ld))

	assertAttr(t, res, "service.name", "edge-service")
	assertAttr(t, res, "host.name", "host-a.example.com")
	assertAttr(t, res, "host.id", "00000000000000000000000000000a01")
	assertAttr(t, res, "process.pid", "1201")
	assertAttr(t, res, "process.command", "sshd")
}

func TestNoOverwrite_ContainerNameOnTheResourceIsLeftAlone(t *testing.T) {
	body := journalBody(t)
	body["_SYSTEMD_UNIT"] = "docker.service"
	body["_COMM"] = "dockerd"
	ld, _ := oneRecord(t, body)
	resourceWith(ld, map[string]string{"container.name": "web-1", "service.name": "web-1", "process.command": "nginx"})

	res, _ := onlyRecord(t, run(t, defaultConfig(), ld))

	assertAttr(t, res, "container.name", "web-1")
	assertAttr(t, res, "service.name", "web-1")
	assertAttr(t, res, "process.command", "nginx")
}

func TestNoOverwrite_EmptyResourceGetsAllFiveAttributes(t *testing.T) {
	ld, _ := oneRecord(t, journalBody(t))

	res, _ := onlyRecord(t, run(t, defaultConfig(), ld))

	if res.Len() != 5 {
		t.Errorf("resource has %d attributes, want 5: %v", res.Len(), res.AsRaw())
	}
	assertAttr(t, res, "service.name", "ssh.service")
}

func TestNoOverwrite_SplitGroupsKeepTheServiceNameOfTheResource(t *testing.T) {
	ld := mixedBatch(t)
	ld.ResourceLogs().At(0).Resource().Attributes().PutStr("service.name", "edge-service")

	out := run(t, defaultConfig(), ld)

	commands := map[string]bool{}
	for i := 0; i < out.ResourceLogs().Len(); i++ {
		attrs := out.ResourceLogs().At(i).Resource().Attributes()
		assertAttr(t, attrs, "service.name", "edge-service")
		if v, ok := attrs.Get("process.command"); ok {
			commands[v.Str()] = true
		}
	}
	if len(commands) != 3 {
		t.Errorf("groups carry commands %v, want the three journal commands", commands)
	}
}

func TestNoOverwrite_SingleTupleSplitPathKeepsTheServiceName(t *testing.T) {
	fixture := loadFixture(t)
	ld := plog.NewLogs()
	rl := ld.ResourceLogs().AppendEmpty()
	rl.Resource().Attributes().PutStr("service.name", "edge-service")
	sl := rl.ScopeLogs().AppendEmpty()
	appendMapRecord(t, sl, fixture[0])
	appendMapRecord(t, sl, fixture[3])

	out := run(t, defaultConfig(), ld)

	if out.ResourceLogs().Len() != 1 {
		t.Fatalf("got %d ResourceLogs, want 1", out.ResourceLogs().Len())
	}
	assertAttr(t, out.ResourceLogs().At(0).Resource().Attributes(), "service.name", "edge-service")
}

func TestNoOverwrite_SplitOffKeepsTheServiceNameAcrossRecords(t *testing.T) {
	cfg := defaultConfig()
	cfg.SplitResources = false
	ld := mixedBatch(t)
	ld.ResourceLogs().At(0).Resource().Attributes().PutStr("service.name", "edge-service")

	out := run(t, cfg, ld)

	assertAttr(t, out.ResourceLogs().At(0).Resource().Attributes(), "service.name", "edge-service")
}

func TestNoOverwrite_SplitOffStillTakesTheLastJournalValueForUnsetAttributes(t *testing.T) {
	cfg := defaultConfig()
	cfg.SplitResources = false
	out := run(t, cfg, mixedBatch(t))

	assertAttr(t, out.ResourceLogs().At(0).Resource().Attributes(), "service.name", "nginx.service")
}

func TestNoOverwrite_SetResourceOffLeavesTheResourceAsItWas(t *testing.T) {
	cfg := &Config{}
	ld, _ := oneRecord(t, journalBody(t))
	resourceWith(ld, map[string]string{"service.name": "edge-service"})

	res, _ := onlyRecord(t, run(t, cfg, ld))

	if res.Len() != 1 {
		t.Errorf("resource changed: %v", res.AsRaw())
	}
}

func TestNoOverwrite_EmptyStringCountsAsMissingAndIsFilled(t *testing.T) {
	ld, _ := oneRecord(t, journalBody(t))
	resourceWith(ld, map[string]string{"service.name": "", "host.name": "", "host.id": "edge-id"})

	res, _ := onlyRecord(t, run(t, defaultConfig(), ld))

	assertAttr(t, res, "service.name", "ssh.service")
	assertAttr(t, res, "host.name", "host-a.example.com")
	assertAttr(t, res, "host.id", "edge-id")
}

func TestNoOverwrite_EmptyStringIsFilledOnTheSplitPathToo(t *testing.T) {
	ld := mixedBatch(t)
	ld.ResourceLogs().At(0).Resource().Attributes().PutStr("service.name", "")

	out := run(t, defaultConfig(), ld)

	units := map[string]bool{}
	for i := 0; i < out.ResourceLogs().Len(); i++ {
		if v, ok := out.ResourceLogs().At(i).Resource().Attributes().Get("service.name"); ok {
			units[v.Str()] = true
		}
	}
	for _, unit := range []string{"ssh.service", "kubelet.service", "nginx.service"} {
		if !units[unit] {
			t.Errorf("no group carries service.name %q: %v", unit, units)
		}
	}
}

func TestNoOverwrite_NonStringValueCountsAsSet(t *testing.T) {
	ld, _ := oneRecord(t, journalBody(t))
	ld.ResourceLogs().At(0).Resource().Attributes().PutInt("process.pid", 77)

	res, _ := onlyRecord(t, run(t, defaultConfig(), ld))

	got, ok := res.Get("process.pid")
	if !ok || got.Type() != pcommon.ValueTypeInt || got.Int() != 77 {
		t.Errorf("process.pid = %v, want the int 77 left alone", got.AsRaw())
	}
}

func TestNoOverwrite_EmptyUnitInTheRecordDoesNotTouchAnOwnedServiceName(t *testing.T) {
	body := journalBody(t)
	body["_SYSTEMD_UNIT"] = ""
	ld, _ := oneRecord(t, body)
	resourceWith(ld, map[string]string{"service.name": "edge-service"})

	res, _ := onlyRecord(t, run(t, defaultConfig(), ld))

	assertAttr(t, res, "service.name", "edge-service")
}

func TestNoOverwrite_RelayedJournalKeepsTheHostNameOfTheResource(t *testing.T) {
	first := map[string]any{"_HOSTNAME": "origin-a", "_COMM": "sshd", "MESSAGE": "one"}
	second := map[string]any{"_HOSTNAME": "origin-b", "_COMM": "sshd", "MESSAGE": "two"}
	ld := plog.NewLogs()
	rl := ld.ResourceLogs().AppendEmpty()
	rl.Resource().Attributes().PutStr("host.name", "relay-host")
	sl := rl.ScopeLogs().AppendEmpty()
	appendMapRecord(t, sl, first)
	appendMapRecord(t, sl, second)

	out := run(t, defaultConfig(), ld)

	for i := 0; i < out.ResourceLogs().Len(); i++ {
		assertAttr(t, out.ResourceLogs().At(i).Resource().Attributes(), "host.name", "relay-host")
	}
}

func TestSplit_AllMappedAttributesOwned_NoSplitAndRecordsStayTogether(t *testing.T) {
	ld := mixedBatch(t)
	resourceWith(ld, map[string]string{
		"host.name": "h", "host.id": "i", "process.pid": "1", "process.command": "c", "service.name": "s",
	})

	out := run(t, defaultConfig(), ld)

	if out.ResourceLogs().Len() != 1 {
		t.Fatalf("got %d ResourceLogs, want 1: every mapped attribute is owned, so nothing can differ", out.ResourceLogs().Len())
	}
	if out.LogRecordCount() != 4 {
		t.Errorf("record count = %d, want 4", out.LogRecordCount())
	}
	recs := out.ResourceLogs().At(0).ScopeLogs().At(0).LogRecords()
	for i := 0; i < recs.Len(); i++ {
		if recs.At(i).Body().Type() != pcommon.ValueTypeStr {
			t.Errorf("record %d body is %v, want the MESSAGE string", i, recs.At(i).Body().Type())
		}
	}
}

func TestSplit_DifferencesInOwnedFieldsDoNotSplit(t *testing.T) {
	base := map[string]any{"_HOSTNAME": "h", "_MACHINE_ID": "m", "_PID": "1", "_COMM": "docker", "MESSAGE": "a"}
	other := map[string]any{"_HOSTNAME": "h", "_MACHINE_ID": "m", "_PID": "1", "_COMM": "docker", "MESSAGE": "b", "_SYSTEMD_UNIT": "x.service"}
	base["_SYSTEMD_UNIT"] = "docker.service"
	ld := plog.NewLogs()
	rl := ld.ResourceLogs().AppendEmpty()
	rl.Resource().Attributes().PutStr("service.name", "edge-service")
	sl := rl.ScopeLogs().AppendEmpty()
	appendMapRecord(t, sl, base)
	appendMapRecord(t, sl, other)

	out := run(t, defaultConfig(), ld)

	if out.ResourceLogs().Len() != 1 {
		t.Fatalf("got %d ResourceLogs, want 1: the units differ but service.name is owned", out.ResourceLogs().Len())
	}
	assertAttr(t, out.ResourceLogs().At(0).Resource().Attributes(), "service.name", "edge-service")
	assertAttr(t, out.ResourceLogs().At(0).Resource().Attributes(), "process.command", "docker")
}

func TestSplit_DifferencesInUnownedFieldsStillSplit(t *testing.T) {
	ld := mixedBatch(t)
	ld.ResourceLogs().At(0).Resource().Attributes().PutStr("service.name", "edge-service")

	out := run(t, defaultConfig(), ld)

	if out.ResourceLogs().Len() < 4 {
		t.Errorf("got %d ResourceLogs, want one per command plus the plain-text group", out.ResourceLogs().Len())
	}
}

func TestConfigValidate_SplitWithoutSetResource_Fails(t *testing.T) {
	cfg := &Config{SplitResources: true}

	if err := cfg.Validate(); err == nil {
		t.Error("expected error: split_resources needs set_resource")
	}
}

func TestDefaultConfig_EnablesEverySwitch(t *testing.T) {
	cfg := defaultConfig()

	if !cfg.SetResource || !cfg.MapSeverity || !cfg.BodyFromMessage || !cfg.SplitResources || !cfg.KeepFields {
		t.Errorf("default config = %+v, want every switch on", cfg)
	}
}
