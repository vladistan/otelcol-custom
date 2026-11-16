// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package journaldprocessor

import (
	"strings"
	"testing"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
)

func fullRecord() map[string]any {
	return map[string]any{
		"_HOSTNAME": "h1", "_MACHINE_ID": "mid", "_PID": "42", "_COMM": "x", "_SYSTEMD_UNIT": "x.service",
		"_EXE": "/usr/bin/x", "_CMDLINE": "x --a", "_UID": "1000", "_GID": "1001",
		"_SYSTEMD_CGROUP": "/system.slice/x.service",
		"_BOOT_ID":        "boot1", "_SYSTEMD_SLICE": "system.slice", "_SYSTEMD_INVOCATION_ID": "inv1", "_TRANSPORT": "journal",
		"SYSLOG_FACILITY": "3", "SYSLOG_IDENTIFIER": "x", "SYSLOG_PID": "42",
		"PRIORITY": "6", "MESSAGE": "hello", "CODE_FILE": "x.c", "_CAP_EFFECTIVE": "0",
	}
}

func extendedConfig() *Config {
	cfg := defaultConfig()
	cfg.MapExtended = true
	return cfg
}

// extendedMappings is the table of the twelve mapped fields.
var extendedMappings = []struct {
	field, attr, value string
	resource           bool
}{
	{"_EXE", "process.executable.path", "/usr/bin/x", true},
	{"_CMDLINE", "process.command_line", "x --a", true},
	{"_UID", "user.id", "1000", true},
	{"_GID", "user.group.id", "1001", true},
	{"_SYSTEMD_CGROUP", "container.id", "/system.slice/x.service", true},
	{"_BOOT_ID", "boot.id", "boot1", false},
	{"_SYSTEMD_SLICE", "systemd.slice", "system.slice", false},
	{"_SYSTEMD_INVOCATION_ID", "systemd.invocation_id", "inv1", false},
	{"_TRANSPORT", "log.source", "journal", false},
	{"SYSLOG_FACILITY", "syslog.facility.code", "3", false},
	{"SYSLOG_IDENTIFIER", "syslog.appname", "x", false},
	{"SYSLOG_PID", "syslog.procid", "42", false},
}

var extendedResourceAttrs = []string{
	"process.executable.path", "process.command_line", "user.id", "user.group.id", "container.id",
}

// batchWith returns one ResourceLogs holding the records, with the preset resource attributes.
func batchWith(t *testing.T, preset map[string]any, records ...map[string]any) plog.Logs {
	t.Helper()
	ld := plog.NewLogs()
	rl := ld.ResourceLogs().AppendEmpty()
	if err := rl.Resource().Attributes().FromRaw(preset); err != nil {
		t.Fatalf("preset attributes: %v", err)
	}
	sl := rl.ScopeLogs().AppendEmpty()
	for _, body := range records {
		appendMapRecord(t, sl, body)
	}
	return ld
}

func attrOf(t *testing.T, m pcommon.Map, key string) (string, bool) {
	t.Helper()
	v, ok := m.Get(key)
	if !ok {
		return "", false
	}
	if v.Type() != pcommon.ValueTypeStr {
		t.Fatalf("attribute %q has type %v, want a string", key, v.Type())
	}
	return v.Str(), true
}

func bothSplitModes(t *testing.T, fn func(t *testing.T, split bool)) {
	t.Helper()
	for _, split := range []bool{true, false} {
		name := "split_off"
		if split {
			name = "split_on"
		}
		t.Run(name, func(t *testing.T) { fn(t, split) })
	}
}

func TestExtended_OffGivesTheReleaseOutput(t *testing.T) {
	ld, _ := oneRecord(t, fullRecord())

	res, lr := onlyRecord(t, run(t, defaultConfig(), ld))

	want := map[string]any{
		"host.name": "h1", "host.id": "mid", "process.pid": "42", "process.command": "x", "service.name": "x.service",
	}
	got := res.AsRaw()
	if len(got) != len(want) {
		t.Fatalf("resource = %v, want exactly %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("resource[%s] = %v, want %v", k, got[k], v)
		}
	}
	if lr.Attributes().Len() != 1 {
		t.Errorf("log attributes = %v, want only journald.fields", lr.Attributes().AsRaw())
	}
	if _, ok := lr.Attributes().Get("journald.fields"); !ok {
		t.Error("journald.fields missing: the default keeps unmapped fields")
	}
}

func TestExtended_MapsEachFieldToItsAttribute(t *testing.T) {
	for _, m := range extendedMappings {
		t.Run(m.attr, func(t *testing.T) {
			ld, _ := oneRecord(t, fullRecord())

			res, lr := onlyRecord(t, run(t, extendedConfig(), ld))

			target := lr.Attributes()
			if m.resource {
				target = res
			}
			got, ok := attrOf(t, target, m.attr)
			if !ok {
				t.Fatalf("%s missing", m.attr)
			}
			if got != m.value {
				t.Errorf("%s = %q, want %q", m.attr, got, m.value)
			}
			other := res
			if m.resource {
				other = lr.Attributes()
			}
			if _, leaked := other.Get(m.attr); leaked {
				t.Errorf("%s set on the wrong level", m.attr)
			}
		})
	}
}

func TestExtended_ContainerIDCarriesTheCgroupPathAsGiven(t *testing.T) {
	ld, _ := oneRecord(t, map[string]any{"_SYSTEMD_CGROUP": "/system.slice/docker-abc.scope", "MESSAGE": "m"})

	res, _ := onlyRecord(t, run(t, extendedConfig(), ld))

	if got, _ := attrOf(t, res, "container.id"); got != "/system.slice/docker-abc.scope" {
		t.Errorf("container.id = %q, want the path unchanged", got)
	}
}

func TestExtended_MissingFieldSetsNothingAndTheOthersStillMap(t *testing.T) {
	ld, _ := oneRecord(t, map[string]any{"_EXE": "/bin/y", "SYSLOG_PID": "7", "MESSAGE": "m"})

	res, lr := onlyRecord(t, run(t, extendedConfig(), ld))

	if got, _ := attrOf(t, res, "process.executable.path"); got != "/bin/y" {
		t.Errorf("process.executable.path = %q, want /bin/y", got)
	}
	if got, _ := attrOf(t, lr.Attributes(), "syslog.procid"); got != "7" {
		t.Errorf("syslog.procid = %q, want 7", got)
	}
	for _, key := range []string{"process.command_line", "user.id", "user.group.id", "container.id"} {
		if _, ok := res.Get(key); ok {
			t.Errorf("resource %s set from a missing field", key)
		}
	}
	for _, key := range []string{"boot.id", "systemd.slice", "log.source", "syslog.appname", "syslog.facility.code"} {
		if _, ok := lr.Attributes().Get(key); ok {
			t.Errorf("log attribute %s set from a missing field", key)
		}
	}
}

func TestExtended_ResourceAttributeAlreadyOnTheResourceIsKept(t *testing.T) {
	bothSplitModes(t, func(t *testing.T, split bool) {
		for _, attr := range extendedResourceAttrs {
			t.Run(attr, func(t *testing.T) {
				cfg := extendedConfig()
				cfg.SplitResources = split
				ld := batchWith(t, map[string]any{attr: "edge-value"}, fullRecord())

				res, _ := onlyRecord(t, run(t, cfg, ld))

				if got, _ := attrOf(t, res, attr); got != "edge-value" {
					t.Errorf("%s = %q, want the edge value kept", attr, got)
				}
				for _, other := range extendedResourceAttrs {
					if other == attr {
						continue
					}
					if _, ok := res.Get(other); !ok {
						t.Errorf("%s missing: only the owned attribute is protected", other)
					}
				}
			})
		}
	})
}

func TestExtended_EmptyResourceAttributesAreFilledFromTheJournal(t *testing.T) {
	bothSplitModes(t, func(t *testing.T, split bool) {
		cfg := extendedConfig()
		cfg.SplitResources = split
		preset := map[string]any{}
		for _, attr := range extendedResourceAttrs {
			preset[attr] = ""
		}
		ld := batchWith(t, preset, fullRecord())

		res, _ := onlyRecord(t, run(t, cfg, ld))

		for _, m := range extendedMappings {
			if !m.resource {
				continue
			}
			if got, _ := attrOf(t, res, m.attr); got != m.value {
				t.Errorf("%s = %q, want %q", m.attr, got, m.value)
			}
		}
	})
}

func TestExtended_SplitGivesEachRecordItsOwnResourceValues(t *testing.T) {
	first, second := fullRecord(), fullRecord()
	second["_EXE"], second["_UID"] = "/usr/bin/y", "2000"
	ld := batchWith(t, map[string]any{}, first, second)

	out := run(t, extendedConfig(), ld)

	if out.ResourceLogs().Len() != 2 {
		t.Fatalf("got %d resources, want 2 (one per _EXE and _UID)", out.ResourceLogs().Len())
	}
	seen := map[string]string{}
	for i := 0; i < out.ResourceLogs().Len(); i++ {
		res := out.ResourceLogs().At(i).Resource().Attributes()
		exe, _ := attrOf(t, res, "process.executable.path")
		uid, _ := attrOf(t, res, "user.id")
		seen[exe] = uid
	}
	if seen["/usr/bin/x"] != "1000" || seen["/usr/bin/y"] != "2000" {
		t.Errorf("resources = %v, want x with 1000 and y with 2000", seen)
	}
}

func TestExtended_SplitOffTheLastRecordSetsTheExtendedResourceAttributes(t *testing.T) {
	first, second := fullRecord(), fullRecord()
	second["_EXE"] = "/usr/bin/y"
	cfg := extendedConfig()
	cfg.SplitResources = false
	ld := batchWith(t, map[string]any{}, first, second)

	res, _ := onlyRecord(t, run(t, cfg, ld))

	if got, _ := attrOf(t, res, "process.executable.path"); got != "/usr/bin/y" {
		t.Errorf("process.executable.path = %q, want the last record's value", got)
	}
}

func TestExtended_RecordsDifferingOnlyInTheExtendedFieldsShareAResourceWhenOff(t *testing.T) {
	first, second := fullRecord(), fullRecord()
	second["_EXE"] = "/usr/bin/y"
	ld := batchWith(t, map[string]any{}, first, second)

	out := run(t, defaultConfig(), ld)

	if out.ResourceLogs().Len() != 1 {
		t.Errorf("got %d resources, want 1: the extended fields are not part of the key when off", out.ResourceLogs().Len())
	}
}

func TestExtended_LogAttributesAreSetPerRecord(t *testing.T) {
	first, second := fullRecord(), fullRecord()
	second["_TRANSPORT"], second["SYSLOG_IDENTIFIER"] = "syslog", "other"
	ld := batchWith(t, map[string]any{}, first, second)

	out := run(t, extendedConfig(), ld)

	got := map[string]string{}
	for i := 0; i < out.ResourceLogs().Len(); i++ {
		recs := out.ResourceLogs().At(i).ScopeLogs().At(0).LogRecords()
		for k := 0; k < recs.Len(); k++ {
			src, _ := attrOf(t, recs.At(k).Attributes(), "log.source")
			app, _ := attrOf(t, recs.At(k).Attributes(), "syslog.appname")
			got[src] = app
		}
	}
	if got["journal"] != "x" || got["syslog"] != "other" {
		t.Errorf("log attributes = %v, want each record its own", got)
	}
}

func TestExtended_MappedFieldsLeaveKeepFields(t *testing.T) {
	ld, _ := oneRecord(t, fullRecord())

	_, lr := onlyRecord(t, run(t, extendedConfig(), ld))

	fields, ok := lr.Attributes().Get("journald.fields")
	if !ok {
		t.Fatal("journald.fields missing")
	}
	kept := fields.Map().AsRaw()
	for _, m := range extendedMappings {
		if _, ok := kept[m.field]; ok {
			t.Errorf("journald.fields still holds %s, which is mapped now", m.field)
		}
	}
	if _, ok := kept["CODE_FILE"]; !ok {
		t.Error("journald.fields lost CODE_FILE, which is not mapped")
	}
	if _, ok := kept["_CAP_EFFECTIVE"]; ok {
		t.Error("journald.fields still holds _CAP_EFFECTIVE, which is mapped now")
	}
}

func TestExtended_OffKeepsTheExtendedFieldsInKeepFields(t *testing.T) {
	ld, _ := oneRecord(t, fullRecord())

	_, lr := onlyRecord(t, run(t, defaultConfig(), ld))

	fields, present := lr.Attributes().Get("journald.fields")
	if !present {
		t.Fatal("journald.fields missing")
	}
	if _, ok := fields.Map().Get("_EXE"); !ok {
		t.Error("journald.fields lost _EXE with map_extended off, as in the release")
	}
}

func containerResource() map[string]any { return map[string]any{"container.name": "web-1"} }

func TestSkipWhenResourceHas_ContainerResourceGetsNoneOfTheFour(t *testing.T) {
	bothSplitModes(t, func(t *testing.T, split bool) {
		cfg := extendedConfig()
		cfg.SplitResources = split
		cfg.SkipWhenResourceHas = []string{"container.name"}
		ld := batchWith(t, containerResource(), fullRecord())

		res, _ := onlyRecord(t, run(t, cfg, ld))

		for _, key := range []string{"process.pid", "process.command", "service.name", "container.id"} {
			if _, ok := res.Get(key); ok {
				t.Errorf("%s set from the journal on a container resource", key)
			}
		}
		for key, want := range map[string]string{"host.name": "h1", "host.id": "mid", "process.executable.path": "/usr/bin/x", "user.id": "1000"} {
			if got, _ := attrOf(t, res, key); got != want {
				t.Errorf("%s = %q, want %q: the skip covers only the four", key, got, want)
			}
		}
	})
}

func TestSkipWhenResourceHas_DefaultEmptyListSetsTheFourAsBefore(t *testing.T) {
	bothSplitModes(t, func(t *testing.T, split bool) {
		cfg := extendedConfig()
		cfg.SplitResources = split
		ld := batchWith(t, containerResource(), fullRecord())

		res, _ := onlyRecord(t, run(t, cfg, ld))

		for key, want := range map[string]string{"process.pid": "42", "process.command": "x", "service.name": "x.service", "container.id": "/system.slice/x.service"} {
			if got, _ := attrOf(t, res, key); got != want {
				t.Errorf("%s = %q, want %q", key, got, want)
			}
		}
	})
}

func TestSkipWhenResourceHas_AttributeNotOnTheResourceChangesNothing(t *testing.T) {
	cfg := extendedConfig()
	cfg.SkipWhenResourceHas = []string{"container.name"}
	ld := batchWith(t, map[string]any{"deployment.environment": "prod"}, fullRecord())

	res, _ := onlyRecord(t, run(t, cfg, ld))

	if got, _ := attrOf(t, res, "service.name"); got != "x.service" {
		t.Errorf("service.name = %q, want x.service", got)
	}
}

func TestSkipWhenResourceHas_EmptyValueIsNotCarried(t *testing.T) {
	cfg := extendedConfig()
	cfg.SkipWhenResourceHas = []string{"container.name"}
	ld := batchWith(t, map[string]any{"container.name": ""}, fullRecord())

	res, _ := onlyRecord(t, run(t, cfg, ld))

	if got, _ := attrOf(t, res, "service.name"); got != "x.service" {
		t.Errorf("service.name = %q: an empty container.name must not block the journal", got)
	}
}

func TestSkipWhenResourceHas_AnyListedAttributeBlocks(t *testing.T) {
	cfg := extendedConfig()
	cfg.SkipWhenResourceHas = []string{"container.name", "k8s.pod.uid"}
	ld := batchWith(t, map[string]any{"k8s.pod.uid": "abc"}, fullRecord())

	res, _ := onlyRecord(t, run(t, cfg, ld))

	if _, ok := res.Get("service.name"); ok {
		t.Error("service.name set although k8s.pod.uid is on the resource")
	}
}

func TestSkipWhenResourceHas_WorksWithoutTheExtendedMapping(t *testing.T) {
	cfg := defaultConfig()
	cfg.SkipWhenResourceHas = []string{"container.name"}
	ld := batchWith(t, containerResource(), fullRecord())

	res, _ := onlyRecord(t, run(t, cfg, ld))

	if _, ok := res.Get("process.pid"); ok {
		t.Error("process.pid set on a container resource")
	}
	if got, _ := attrOf(t, res, "host.name"); got != "h1" {
		t.Errorf("host.name = %q, want h1", got)
	}
}

func underscoreBody() map[string]any {
	return map[string]any{
		"_HOSTNAME": "h1", "_EXE": "/usr/bin/x", "__CURSOR": "c1", "_STREAM_ID": "s1", "CODE_FILE": "x.c", "PRIORITY": "6",
		"SYSLOG_IDENTIFIER": "x",
	}
}

func TestDropUnmappedUnderscore_BodyWithoutMessageLosesItsUnderscoreKeys(t *testing.T) {
	cfg := extendedConfig()
	cfg.DropUnmappedUnderscore = true
	ld, _ := oneRecord(t, underscoreBody())

	_, lr := onlyRecord(t, run(t, cfg, ld))

	got := lr.Body().Map().AsRaw()
	for key := range got {
		if strings.HasPrefix(key, "_") {
			t.Errorf("body keeps %q", key)
		}
	}
	if len(got) != 1 || got["CODE_FILE"] != "x.c" {
		t.Errorf("body = %v, want only CODE_FILE: PRIORITY and SYSLOG_* leave the body with map_extended", got)
	}
}

func TestDropUnmappedUnderscore_DefaultKeepsTheMappedUnderscoreKeys(t *testing.T) {
	ld, _ := oneRecord(t, underscoreBody())

	_, lr := onlyRecord(t, run(t, extendedConfig(), ld))

	got := lr.Body().Map().AsRaw()
	for _, key := range []string{"_HOSTNAME", "_EXE"} {
		if _, ok := got[key]; !ok {
			t.Errorf("body lost %q with the default false", key)
		}
	}
	for _, key := range []string{"__CURSOR", "_STREAM_ID"} {
		if _, ok := got[key]; ok {
			t.Errorf("body keeps %q, which map_extended drops", key)
		}
	}
}

func TestDropUnmappedUnderscore_BodyThatKeepsItsMessageIsNotTouched(t *testing.T) {
	cfg := extendedConfig()
	cfg.DropUnmappedUnderscore = true
	cfg.BodyFromMessage = false
	body := underscoreBody()
	body["MESSAGE"] = "hello"
	ld, _ := oneRecord(t, body)

	_, lr := onlyRecord(t, run(t, cfg, ld))

	if _, ok := lr.Body().Map().Get("_HOSTNAME"); !ok {
		t.Error("a map body with a MESSAGE lost its underscore keys")
	}
}

func TestDropUnmappedUnderscore_BodyFromMessageStillWins(t *testing.T) {
	cfg := extendedConfig()
	cfg.DropUnmappedUnderscore = true
	body := underscoreBody()
	body["MESSAGE"] = "hello"
	ld, _ := oneRecord(t, body)

	_, lr := onlyRecord(t, run(t, cfg, ld))

	if lr.Body().Type() != pcommon.ValueTypeStr || lr.Body().Str() != "hello" {
		t.Errorf("body = %v, want the message string", lr.Body().AsRaw())
	}
}

func TestDropUnmappedUnderscore_MappingAndKeepFieldsStillSeeTheFields(t *testing.T) {
	cfg := extendedConfig()
	cfg.DropUnmappedUnderscore = true
	body := underscoreBody()
	ld, _ := oneRecord(t, body)

	res, lr := onlyRecord(t, run(t, cfg, ld))

	if got, _ := attrOf(t, res, "process.executable.path"); got != "/usr/bin/x" {
		t.Errorf("process.executable.path = %q: the mapping reads the field before the drop", got)
	}
	fields, present := lr.Attributes().Get("journald.fields")
	if !present {
		t.Fatal("journald.fields missing")
	}
	if _, ok := fields.Map().Get("CODE_FILE"); !ok {
		t.Error("journald.fields lost CODE_FILE: only the body is stripped")
	}
}

func TestDropUnmappedUnderscore_WorksWithoutTheExtendedMapping(t *testing.T) {
	cfg := defaultConfig()
	cfg.DropUnmappedUnderscore = true
	ld, _ := oneRecord(t, underscoreBody())

	_, lr := onlyRecord(t, run(t, cfg, ld))

	if _, ok := lr.Body().Map().Get("__CURSOR"); ok {
		t.Error("__CURSOR stays in the body")
	}
}

func TestExtended_NonMapBodyPassesUnchanged(t *testing.T) {
	cfg := extendedConfig()
	cfg.DropUnmappedUnderscore = true
	ld := plog.NewLogs()
	rl := ld.ResourceLogs().AppendEmpty()
	rl.ScopeLogs().AppendEmpty().LogRecords().AppendEmpty().Body().SetStr("plain line")

	out := run(t, cfg, ld)

	if out.ResourceLogs().Len() != 1 || out.ResourceLogs().At(0).Resource().Attributes().Len() != 0 {
		t.Errorf("output = %d resources with attributes %v", out.ResourceLogs().Len(), out.ResourceLogs().At(0).Resource().Attributes().AsRaw())
	}
	if got := out.ResourceLogs().At(0).ScopeLogs().At(0).LogRecords().At(0).Body().Str(); got != "plain line" {
		t.Errorf("body = %q, want it unchanged", got)
	}
}

func TestConfig_NewKeysAreOffByDefault(t *testing.T) {
	cfg := defaultConfig()

	if cfg.MapExtended || cfg.DropUnmappedUnderscore || len(cfg.SkipWhenResourceHas) != 0 {
		t.Errorf("defaults = %+v, want map_extended, drop_unmapped_underscore and skip_when_resource_has off", cfg)
	}
}

func TestConfig_ValidateRejectsTheNewKeysWithoutSetResource(t *testing.T) {
	cases := map[string]func(*Config){
		"map_extended":                func(c *Config) { c.MapExtended = true },
		"skip_when_resource_has":      func(c *Config) { c.SkipWhenResourceHas = []string{"container.name"} },
		"split_resources (as before)": func(c *Config) { c.SplitResources = true },
	}
	for name, set := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := &Config{}
			set(cfg)

			if err := cfg.Validate(); err == nil {
				t.Errorf("%s without set_resource validated", name)
			}
		})
	}
}

func TestConfig_ValidateRejectsAnEmptySkipName(t *testing.T) {
	cfg := defaultConfig()
	cfg.SkipWhenResourceHas = []string{"container.name", ""}

	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "skip_when_resource_has[1] is empty") {
		t.Errorf("error = %v, want one that names skip_when_resource_has[1]", err)
	}
}

func TestConfig_ValidateAcceptsTheNewKeysWithSetResource(t *testing.T) {
	cfg := extendedConfig()
	cfg.SkipWhenResourceHas = []string{"container.name"}
	cfg.DropUnmappedUnderscore = true

	if err := cfg.Validate(); err != nil {
		t.Errorf("valid config rejected: %v", err)
	}
}

func TestDropUnmappedUnderscore_AloneNeedsNoSetResource(t *testing.T) {
	cfg := &Config{DropUnmappedUnderscore: true}

	if err := cfg.Validate(); err != nil {
		t.Errorf("drop_unmapped_underscore alone rejected: %v", err)
	}
}
