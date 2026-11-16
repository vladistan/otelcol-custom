// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package journaldprocessor

import (
	"context"
	"testing"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/processor/processortest"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

const selinuxContext = "system_u:system_r:unconfined_t:s0\ns0-s0:c0.c1023"

// withExtras adds the journal fields of the deployed OTTL's extras to a record.
func withExtras(body map[string]any) map[string]any {
	body["_RUNTIME_SCOPE"] = "container"
	body["_SELINUX_CONTEXT"] = selinuxContext
	body["_CAP_EFFECTIVE"] = "1ffffffffff"
	return body
}

// withoutMessage returns a copy of the record without MESSAGE.
func withoutMessage(body map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range body {
		if k != fieldMessage {
			out[k] = v
		}
	}
	return out
}

// keepBodyConfig keeps the map body, so a test can look at what the processor left in it.
func keepBodyConfig() *Config {
	cfg := extendedConfig()
	cfg.BodyFromMessage = false
	return cfg
}

func bodyMap(t *testing.T, lr plog.LogRecord) pcommon.Map {
	t.Helper()
	if lr.Body().Type() != pcommon.ValueTypeMap {
		t.Fatalf("body type = %v, want a map", lr.Body().Type())
	}
	return lr.Body().Map()
}

func mapAttr(t *testing.T, m pcommon.Map, key string) (pcommon.Map, bool) {
	t.Helper()
	v, ok := m.Get(key)
	if !ok {
		return pcommon.Map{}, false
	}
	if v.Type() != pcommon.ValueTypeMap {
		t.Fatalf("attribute %q has type %v, want a map", key, v.Type())
	}
	return v.Map(), true
}

// has is false for a map that is absent (the zero value) and for a missing key.
func has(m pcommon.Map, key string) bool {
	if m == (pcommon.Map{}) {
		return false
	}
	_, ok := m.Get(key)
	return ok
}

var recordShapes = []struct {
	name   string
	record func() map[string]any
}{
	{"with MESSAGE", func() map[string]any { return withExtras(fullRecord()) }},
	{"without MESSAGE", func() map[string]any { return withoutMessage(withExtras(fullRecord())) }},
}

func TestParityExtras_RuntimeSelinuxAndCapabilitiesAreMapped(t *testing.T) {
	for _, shape := range recordShapes {
		t.Run(shape.name, func(t *testing.T) {
			ld, _ := oneRecord(t, shape.record())

			res, lr := onlyRecord(t, run(t, extendedConfig(), ld))

			want := []struct {
				target pcommon.Map
				key    string
				value  string
			}{
				{res, "container.runtime", "container"},
				{lr.Attributes(), "selinux.context", selinuxContext},
				{lr.Attributes(), "process.capabilities.effective", "1ffffffffff"},
			}
			for _, w := range want {
				got, ok := attrOf(t, w.target, w.key)
				if !ok || got != w.value {
					t.Errorf("%s = %q (present %v), want %q", w.key, got, ok, w.value)
				}
			}
			if _, leaked := lr.Attributes().Get("container.runtime"); leaked {
				t.Error("container.runtime set on the log record, want the resource")
			}
		})
	}
}

func TestParityExtras_OffMapsNone(t *testing.T) {
	ld, _ := oneRecord(t, withExtras(fullRecord()))

	res, lr := onlyRecord(t, run(t, defaultConfig(), ld))

	for _, key := range []string{"container.runtime", "selinux.context", "process.capabilities.effective", "journald.unknown_fields"} {
		if _, ok := res.Get(key); ok {
			t.Errorf("resource has %s with map_extended off", key)
		}
		if _, ok := lr.Attributes().Get(key); ok {
			t.Errorf("log record has %s with map_extended off", key)
		}
	}
}

func TestParityExtras_ContainerRuntimeKeepsWhatTheResourceHas(t *testing.T) {
	bothSplitModes(t, func(t *testing.T, split bool) {
		cases := []struct {
			name   string
			preset map[string]any
			want   string
		}{
			{"a value stays", map[string]any{"container.runtime": "podman"}, "podman"},
			{"an empty string counts as missing", map[string]any{"container.runtime": ""}, "container"},
			{"a missing attribute is set", map[string]any{}, "container"},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				cfg := extendedConfig()
				cfg.SplitResources = split
				ld := batchWith(t, c.preset, withExtras(fullRecord()))

				res, _ := onlyRecord(t, run(t, cfg, ld))

				if got, _ := attrOf(t, res, "container.runtime"); got != c.want {
					t.Errorf("container.runtime = %q, want %q", got, c.want)
				}
			})
		}
	})
}

func TestParityExtras_PriorityAndSyslogKeysLeaveTheOutput(t *testing.T) {
	for _, shape := range recordShapes {
		t.Run(shape.name, func(t *testing.T) {
			ld, _ := oneRecord(t, shape.record())

			_, lr := onlyRecord(t, run(t, keepBodyConfig(), ld))

			body := bodyMap(t, lr)
			fields, _ := mapAttr(t, lr.Attributes(), attrFields)
			for _, key := range []string{"PRIORITY", "SYSLOG_FACILITY", "SYSLOG_IDENTIFIER", "SYSLOG_PID"} {
				if has(body, key) {
					t.Errorf("body still has %s", key)
				}
				if has(fields, key) {
					t.Errorf("journald.fields still has %s", key)
				}
			}
			if lr.SeverityText() != "INFO" {
				t.Errorf("severity text = %q, want INFO: PRIORITY is read before it is removed", lr.SeverityText())
			}
			if got, _ := attrOf(t, lr.Attributes(), "syslog.appname"); got != "x" {
				t.Errorf("syslog.appname = %q, want x: the attribute is set before the key is removed", got)
			}
		})
	}
}

func TestParityExtras_OffKeepsPriorityAndSyslogKeysInTheBody(t *testing.T) {
	cfg := defaultConfig()
	cfg.BodyFromMessage = false
	ld, _ := oneRecord(t, fullRecord())

	_, lr := onlyRecord(t, run(t, cfg, ld))

	body := bodyMap(t, lr)
	for _, key := range []string{"PRIORITY", "SYSLOG_FACILITY", "SYSLOG_IDENTIFIER", "SYSLOG_PID"} {
		if !has(body, key) {
			t.Errorf("body lost %s with map_extended off", key)
		}
	}
}

func TestParityExtras_UnmappedUnderscoreKeysMoveToUnknownFields(t *testing.T) {
	for _, shape := range recordShapes {
		t.Run(shape.name, func(t *testing.T) {
			record := shape.record()
			record["_FOO"] = "foo"
			record["__BAR"] = "bar"
			ld, _ := oneRecord(t, record)

			_, lr := onlyRecord(t, run(t, keepBodyConfig(), ld))

			unknown, ok := mapAttr(t, lr.Attributes(), "journald.unknown_fields")
			if !ok {
				t.Fatal("journald.unknown_fields missing")
			}
			if got := unknown.AsRaw(); len(got) != 2 || got["_FOO"] != "foo" || got["__BAR"] != "bar" {
				t.Errorf("journald.unknown_fields = %v, want _FOO and __BAR only", got)
			}
			fields, _ := mapAttr(t, lr.Attributes(), attrFields)
			body := bodyMap(t, lr)
			for _, key := range []string{"_FOO", "__BAR"} {
				if has(fields, key) {
					t.Errorf("journald.fields still has %s", key)
				}
				if has(body, key) {
					t.Errorf("body still has %s", key)
				}
			}
		})
	}
}

func TestParityExtras_NoUnmappedUnderscoreKeyGivesNoUnknownFields(t *testing.T) {
	ld, _ := oneRecord(t, withExtras(fullRecord()))

	_, lr := onlyRecord(t, run(t, extendedConfig(), ld))

	if _, ok := lr.Attributes().Get("journald.unknown_fields"); ok {
		t.Error("journald.unknown_fields set although every _ key is mapped")
	}
}

func TestParityExtras_FourInternalKeysAreDroppedEverywhere(t *testing.T) {
	internal := []string{"_SOURCE_REALTIME_TIMESTAMP", "__CURSOR", "__MONOTONIC_TIMESTAMP", "_STREAM_ID"}
	record := withExtras(fullRecord())
	for _, key := range internal {
		record[key] = "v"
	}
	ld, _ := oneRecord(t, record)

	_, lr := onlyRecord(t, run(t, keepBodyConfig(), ld))

	fields, _ := mapAttr(t, lr.Attributes(), attrFields)
	unknown, hasUnknown := mapAttr(t, lr.Attributes(), "journald.unknown_fields")
	body := bodyMap(t, lr)
	for _, key := range internal {
		if has(fields, key) {
			t.Errorf("journald.fields has %s", key)
		}
		if has(body, key) {
			t.Errorf("body has %s", key)
		}
		if has(unknown, key) {
			t.Errorf("journald.unknown_fields has %s", key)
		}
	}
	if hasUnknown {
		t.Error("journald.unknown_fields set, want none: only internal keys were left over")
	}
}

func TestParityExtras_OffLeavesUnderscoreKeysInJournaldFields(t *testing.T) {
	record := fullRecord()
	record["_FOO"] = "foo"
	record["__CURSOR"] = "c"
	ld, _ := oneRecord(t, record)

	_, lr := onlyRecord(t, run(t, defaultConfig(), ld))

	fields, _ := mapAttr(t, lr.Attributes(), attrFields)
	for _, key := range []string{"_FOO", "__CURSOR"} {
		if !has(fields, key) {
			t.Errorf("journald.fields lost %s with map_extended off", key)
		}
	}
	if _, ok := lr.Attributes().Get("journald.unknown_fields"); ok {
		t.Error("journald.unknown_fields set with map_extended off")
	}
}

func TestParityExtras_DropUnmappedUnderscoreFindsNothingLeftToDrop(t *testing.T) {
	record := withoutMessage(withExtras(fullRecord()))
	record["_FOO"] = "foo"
	cfg := keepBodyConfig()
	cfg.DropUnmappedUnderscore = true
	ld, _ := oneRecord(t, record)

	_, lr := onlyRecord(t, run(t, cfg, ld))

	unknown, ok := mapAttr(t, lr.Attributes(), "journald.unknown_fields")
	if !ok || unknown.AsRaw()["_FOO"] != "foo" {
		t.Errorf("journald.unknown_fields = %v (present %v), want _FOO kept there before the drop", unknown.AsRaw(), ok)
	}
	bodyMap(t, lr).Range(func(k string, _ pcommon.Value) bool {
		if len(k) > 0 && k[0] == '_' {
			t.Errorf("body still has %s", k)
		}
		return true
	})
}

// createdWarnings returns the Warn messages the factory logs when it creates the processor.
func createdWarnings(t *testing.T, cfg *Config) []string {
	t.Helper()
	core, logs := observer.New(zapcore.WarnLevel)
	set := processortest.NewNopSettings(component.MustNewType(typeStr))
	set.Logger = zap.New(core)
	if _, err := NewFactory().CreateLogs(context.Background(), set, cfg, new(consumertest.LogsSink)); err != nil {
		t.Fatalf("create: %v", err)
	}
	var out []string
	for _, entry := range logs.All() {
		out = append(out, entry.Message)
	}
	return out
}

func TestParityExtras_WarnsWhenMapExtendedRunsWithoutSplitResources(t *testing.T) {
	cases := []struct {
		name     string
		mutate   func(*Config)
		wantWarn bool
	}{
		{"extended with split off", func(c *Config) { c.MapExtended, c.SplitResources = true, false }, true},
		{"extended with split on", func(c *Config) { c.MapExtended = true }, false},
		{"split off without extended", func(c *Config) { c.SplitResources = false }, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := defaultConfig()
			c.mutate(cfg)

			warnings := createdWarnings(t, cfg)

			if got := len(warnings) > 0; got != c.wantWarn {
				t.Errorf("warnings = %v, want a warning: %v", warnings, c.wantWarn)
			}
		})
	}
}
