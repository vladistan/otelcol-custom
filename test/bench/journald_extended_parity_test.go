// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package bench

import (
	"context"
	"fmt"
	"maps"
	"os"
	"testing"

	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/processor"
	"go.opentelemetry.io/collector/processor/processortest"
	"gopkg.in/yaml.v3"

	"github.com/vladistan/otelcol-custom/processor/journaldprocessor"
)

// liveBlockText is the gateway block that journaldprocessor replaces, as deployed.
const liveBlockText = "testdata/gateway-transform-journald.yaml"

var (
	extendedResource = []string{"process.executable.path", "process.command_line", "user.id", "user.group.id", "container.id"}
	extendedLog      = []string{
		"boot.id", "systemd.slice", "systemd.invocation_id", "log.source",
		"syslog.facility.code", "syslog.appname", "syslog.procid",
	}
	baseResource = []string{"host.name", "host.id", "process.pid", "process.command", "service.name"}
)

func newLiveOTTL(t testing.TB, sink consumer.Logs) processor.Logs {
	t.Helper()
	raw, err := os.ReadFile(liveBlockText)
	if err != nil {
		t.Fatalf("read the live block: %v", err)
	}
	var doc map[string]map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse the live block: %v", err)
	}
	block, ok := doc[ottlBlock]
	if !ok {
		t.Fatalf("%s not found in %s", ottlBlock, liveBlockText)
	}
	return newOTTLFromBlock(t, sink, block)
}

// newReplacement is journaldprocessor as the gateway runs it in place of the block.
func newReplacement(t testing.TB, sink consumer.Logs, skip ...string) processor.Logs {
	t.Helper()
	return newReplacementWith(t, sink, func(cfg *journaldprocessor.Config) { cfg.SkipWhenResourceHas = skip })
}

// newReplacementWith starts from the parity config and lets a test change it.
func newReplacementWith(t testing.TB, sink consumer.Logs, change func(*journaldprocessor.Config)) processor.Logs {
	t.Helper()
	factory := journaldprocessor.NewFactory()
	cfg := factory.CreateDefaultConfig().(*journaldprocessor.Config)
	cfg.KeepFields = false
	cfg.MapExtended = true
	change(cfg)
	proc, err := factory.CreateLogs(context.Background(), processortest.NewNopSettings(factory.Type()), cfg, sink)
	if err != nil {
		t.Fatalf("create journaldprocessor: %v", err)
	}
	return proc
}

func allFieldsRecord(unit string) map[string]any {
	return map[string]any{
		"_HOSTNAME": "h1", "_MACHINE_ID": "mid", "_PID": "42", "_COMM": "x", "_SYSTEMD_UNIT": unit,
		"_EXE": "/usr/bin/" + unit, "_CMDLINE": unit + " --a", "_UID": "1000", "_GID": "1001",
		"_SYSTEMD_CGROUP": "/system.slice/" + unit, "_BOOT_ID": "boot1", "_SYSTEMD_SLICE": "system.slice",
		"_SYSTEMD_INVOCATION_ID": "inv-" + unit, "_TRANSPORT": "journal",
		"SYSLOG_FACILITY": "3", "SYSLOG_IDENTIFIER": unit, "SYSLOG_PID": "42",
		"PRIORITY": "6", "MESSAGE": "m-" + unit, "CODE_FILE": "x.c",
	}
}

// mapped lists the base and the twelve extended attributes a record ended with.
func mapped(res pcommon.Map, lr plog.LogRecord) map[string]any {
	out := map[string]any{}
	for _, key := range append(append([]string{}, baseResource...), extendedResource...) {
		if v, ok := res.Get(key); ok {
			out["resource:"+key] = v.AsRaw()
		}
	}
	for _, key := range extendedLog {
		if v, ok := lr.Attributes().Get(key); ok {
			out["log:"+key] = v.AsRaw()
		}
	}
	return out
}

func sameMapped(t *testing.T, label string, got, want map[string]any) {
	t.Helper()
	if !maps.Equal(got, want) {
		t.Errorf("%s:\n journaldprocessor %v\n live block        %v", label, got, want)
	}
}

func bareBatch(t *testing.T, preset map[string]any, records ...map[string]any) plog.Logs {
	t.Helper()
	ld := batchOf(t, records...)
	if err := ld.ResourceLogs().At(0).Resource().Attributes().FromRaw(preset); err != nil {
		t.Fatalf("preset: %v", err)
	}
	return ld
}

func TestJournaldExtendedParity_AllFieldsRecordMatchesTheLiveBlock(t *testing.T) {
	ottlSink, procSink := new(consumertest.LogsSink), new(consumertest.LogsSink)
	ottl, replacement := newLiveOTTL(t, ottlSink), newReplacement(t, procSink)
	record := allFieldsRecord("x.service")

	resO, lrO := singleRecord(t, runThrough(t, ottl, ottlSink, bareBatch(t, nil, record)))
	resP, lrP := singleRecord(t, runThrough(t, replacement, procSink, bareBatch(t, nil, record)))

	sameMapped(t, "all fields", mapped(resP, lrP), mapped(resO, lrO))
}

func TestJournaldExtendedParity_RecordWithoutMessageMatchesOnTheMappedAttributes(t *testing.T) {
	ottlSink, procSink := new(consumertest.LogsSink), new(consumertest.LogsSink)
	ottl, replacement := newLiveOTTL(t, ottlSink), newReplacement(t, procSink)
	record := allFieldsRecord("x.service")
	delete(record, "MESSAGE")

	resO, lrO := singleRecord(t, runThrough(t, ottl, ottlSink, bareBatch(t, nil, record)))
	resP, lrP := singleRecord(t, runThrough(t, replacement, procSink, bareBatch(t, nil, record)))

	sameMapped(t, "no MESSAGE", mapped(resP, lrP), mapped(resO, lrO))
}

// extrasRecord adds the fields the deployed block writes beyond the twelve attributes.
func extrasRecord(withMessage bool) map[string]any {
	record := allFieldsRecord("x.service")
	record["_RUNTIME_SCOPE"] = "container"
	record["_SELINUX_CONTEXT"] = "system_u:system_r:unconfined_t:s0\ns0-s0:c0.c1023"
	record["_CAP_EFFECTIVE"] = "1ffffffffff"
	record["_FOO"] = "foo"
	record["__CURSOR"] = "c1"
	record["_STREAM_ID"] = "s1"
	if !withMessage {
		delete(record, "MESSAGE")
	}
	return record
}

var extrasKeys = struct{ resource, log []string }{
	resource: []string{"container.runtime"},
	log:      []string{"selinux.context", "process.capabilities.effective"},
}

func extras(res pcommon.Map, lr plog.LogRecord) map[string]any {
	out := map[string]any{}
	for _, key := range extrasKeys.resource {
		if v, ok := res.Get(key); ok {
			out["resource:"+key] = v.AsRaw()
		}
	}
	for _, key := range extrasKeys.log {
		if v, ok := lr.Attributes().Get(key); ok {
			out["log:"+key] = v.AsRaw()
		}
	}
	return out
}

func dropUnderscore(cfg *journaldprocessor.Config) { cfg.DropUnmappedUnderscore = true }

func TestJournaldExtendedParity_ExtrasOnARecordWithoutMessageMatchTheLiveBlock(t *testing.T) {
	ottlSink, procSink := new(consumertest.LogsSink), new(consumertest.LogsSink)
	ottl, replacement := newLiveOTTL(t, ottlSink), newReplacementWith(t, procSink, dropUnderscore)
	record := extrasRecord(false)

	resO, lrO := singleRecord(t, runThrough(t, ottl, ottlSink, bareBatch(t, nil, record)))
	resP, lrP := singleRecord(t, runThrough(t, replacement, procSink, bareBatch(t, nil, record)))

	sameMapped(t, "extras, no MESSAGE", extras(resP, lrP), extras(resO, lrO))
	if len(extras(resP, lrP)) != 3 {
		t.Errorf("extras = %v, want container.runtime, selinux.context and process.capabilities.effective", extras(resP, lrP))
	}
	for _, key := range []string{"PRIORITY", "SYSLOG_FACILITY", "SYSLOG_IDENTIFIER", "SYSLOG_PID", "__CURSOR", "_STREAM_ID", "_FOO"} {
		if _, ok := lrO.Body().Map().Get(key); ok {
			t.Errorf("the live block left %s in the body", key)
		}
		if _, ok := lrP.Body().Map().Get(key); ok {
			t.Errorf("journaldprocessor left %s in the body", key)
		}
	}
	if got, want := lrP.Body().Map().AsRaw(), lrO.Body().Map().AsRaw(); !maps.Equal(got, want) {
		t.Errorf("body without MESSAGE:\n journaldprocessor %v\n live block        %v", got, want)
	}
}

// The block writes journald.unknown_fields as an empty map; the processor writes the leftover keys.
func TestJournaldExtendedParity_UnknownFieldsDifferByDesign(t *testing.T) {
	ottlSink, procSink := new(consumertest.LogsSink), new(consumertest.LogsSink)
	ottl, replacement := newLiveOTTL(t, ottlSink), newReplacementWith(t, procSink, dropUnderscore)
	record := extrasRecord(false)

	_, lrO := singleRecord(t, runThrough(t, ottl, ottlSink, bareBatch(t, nil, record)))
	_, lrP := singleRecord(t, runThrough(t, replacement, procSink, bareBatch(t, nil, record)))

	live, ok := lrO.Attributes().Get("journald.unknown_fields")
	if !ok || live.Type() != pcommon.ValueTypeMap || live.Map().Len() != 0 {
		t.Errorf("live block journald.unknown_fields = %v (present %v), want an empty map", live.AsRaw(), ok)
	}
	got, ok := lrP.Attributes().Get("journald.unknown_fields")
	if !ok || !maps.Equal(got.Map().AsRaw(), map[string]any{"_FOO": "foo"}) {
		t.Errorf("journaldprocessor journald.unknown_fields = %v (present %v), want only _FOO", got.AsRaw(), ok)
	}
}

// With a MESSAGE the block never reaches the extras; the processor sets them.
func TestJournaldExtendedParity_ExtrasOnARecordWithMessageAreSetOnlyByTheProcessor(t *testing.T) {
	ottlSink, procSink := new(consumertest.LogsSink), new(consumertest.LogsSink)
	ottl, replacement := newLiveOTTL(t, ottlSink), newReplacementWith(t, procSink, dropUnderscore)
	record := extrasRecord(true)

	resO, lrO := singleRecord(t, runThrough(t, ottl, ottlSink, bareBatch(t, nil, record)))
	resP, lrP := singleRecord(t, runThrough(t, replacement, procSink, bareBatch(t, nil, record)))

	if got := extras(resO, lrO); len(got) != 0 {
		t.Errorf("the live block set %v on a record with MESSAGE, expected none", got)
	}
	if got := extras(resP, lrP); len(got) != 3 {
		t.Errorf("journaldprocessor set %v on a record with MESSAGE, want all three", got)
	}
}

func TestJournaldExtendedParity_ContainerResourceMatchesWithTheSkipKey(t *testing.T) {
	ottlSink, procSink := new(consumertest.LogsSink), new(consumertest.LogsSink)
	ottl, replacement := newLiveOTTL(t, ottlSink), newReplacement(t, procSink, "container.name")
	preset := map[string]any{"container.name": "web-1"}
	record := allFieldsRecord("docker.service")

	resO, lrO := singleRecord(t, runThrough(t, ottl, ottlSink, bareBatch(t, preset, record)))
	resP, lrP := singleRecord(t, runThrough(t, replacement, procSink, bareBatch(t, preset, record)))

	got, want := mapped(resP, lrP), mapped(resO, lrO)
	sameMapped(t, "container resource", got, want)
	for _, key := range []string{"process.pid", "process.command", "service.name", "container.id"} {
		if _, ok := got["resource:"+key]; ok {
			t.Errorf("%s set on a container resource", key)
		}
	}
}

func TestJournaldExtendedParity_ContainerResourceWithoutTheSkipKeyDiffers(t *testing.T) {
	ottlSink, procSink := new(consumertest.LogsSink), new(consumertest.LogsSink)
	ottl, replacement := newLiveOTTL(t, ottlSink), newReplacement(t, procSink)
	preset := map[string]any{"container.name": "web-1"}
	record := allFieldsRecord("docker.service")

	resO, _ := singleRecord(t, runThrough(t, ottl, ottlSink, bareBatch(t, preset, record)))
	resP, _ := singleRecord(t, runThrough(t, replacement, procSink, bareBatch(t, preset, record)))

	if _, ok := resO.Get("process.pid"); ok {
		t.Error("the live block set process.pid on a container resource")
	}
	if _, ok := resP.Get("process.pid"); !ok {
		t.Error("without skip_when_resource_has the processor must set process.pid, as before")
	}
}

func TestJournaldExtendedParity_EdgeSetHostIDMatchesAndEdgeSetExtendedKeepsTheEdgeValue(t *testing.T) {
	ottlSink, procSink := new(consumertest.LogsSink), new(consumertest.LogsSink)
	ottl, replacement := newLiveOTTL(t, ottlSink), newReplacement(t, procSink)
	record := allFieldsRecord("x.service")

	resO, lrO := singleRecord(t, runThrough(t, ottl, ottlSink, bareBatch(t, map[string]any{"host.id": "cloud-id"}, record)))
	resP, lrP := singleRecord(t, runThrough(t, replacement, procSink, bareBatch(t, map[string]any{"host.id": "cloud-id"}, record)))
	sameMapped(t, "edge host.id", mapped(resP, lrP), mapped(resO, lrO))

	resO, _ = singleRecord(t, runThrough(t, ottl, ottlSink, bareBatch(t, map[string]any{"user.id": "edge"}, record)))
	resP, _ = singleRecord(t, runThrough(t, replacement, procSink, bareBatch(t, map[string]any{"user.id": "edge"}, record)))
	if v, _ := resO.Get("user.id"); v.AsString() != "1000" {
		t.Errorf("the live block gave user.id %q, expected it to overwrite with 1000", v.AsString())
	}
	if v, _ := resP.Get("user.id"); v.AsString() != "edge" {
		t.Errorf("journaldprocessor gave user.id %q, want the edge value kept", v.AsString())
	}
}

func TestJournaldExtendedParity_TwoUnitsInOneResource_LogAttributesMatchAndResourcesAreOwnPerRecord(t *testing.T) {
	ottlSink, procSink := new(consumertest.LogsSink), new(consumertest.LogsSink)
	ottl, replacement := newLiveOTTL(t, ottlSink), newReplacement(t, procSink)
	a, b := allFieldsRecord("a.service"), allFieldsRecord("b.service")

	outO := runThrough(t, ottl, ottlSink, bareBatch(t, nil, a, b))
	outP := runThrough(t, replacement, procSink, bareBatch(t, nil, a, b))

	if outO.ResourceLogs().Len() != 1 {
		t.Fatalf("live block gave %d resources, want one shared", outO.ResourceLogs().Len())
	}
	if outP.ResourceLogs().Len() != 2 {
		t.Fatalf("journaldprocessor gave %d resources, want one per unit", outP.ResourceLogs().Len())
	}
	// The block writes the resource once per record, so both end with the last record's values.
	sharedExe, _ := outO.ResourceLogs().At(0).Resource().Attributes().Get("process.executable.path")
	if sharedExe.AsString() != "/usr/bin/b.service" {
		t.Errorf("live block resource exe = %q, want the last record's", sharedExe.AsString())
	}
	byUnit := map[string]map[string]any{}
	for i := 0; i < outP.ResourceLogs().Len(); i++ {
		rl := outP.ResourceLogs().At(i)
		lr := rl.ScopeLogs().At(0).LogRecords().At(0)
		byUnit[lr.Body().AsString()] = mapped(rl.Resource().Attributes(), lr)
	}
	for _, unit := range []string{"a.service", "b.service"} {
		got := byUnit["m-"+unit]
		if got["resource:process.executable.path"] != "/usr/bin/"+unit || got["resource:service.name"] != unit {
			t.Errorf("%s resource = %v, want its own values", unit, got)
		}
		if got["log:syslog.appname"] != unit || got["log:systemd.invocation_id"] != "inv-"+unit {
			t.Errorf("%s log attributes = %v, want its own values", unit, got)
		}
	}
}

// leftoverFields are keys a real journal record often carries that no mapping reads.
var leftoverFields = map[string]any{
	"_AUDIT_SESSION": "3", "_AUDIT_LOGINUID": "1000", "_SYSTEMD_USER_SLICE": "-.slice", "_SYSTEMD_OWNER_UID": "1000",
}

func journaldExtendedBatch(b *testing.B, units int, leftovers bool) plog.Logs {
	b.Helper()
	ld := plog.NewLogs()
	sl := ld.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty()
	for i := 0; i < batchSize; i++ {
		body := allFieldsRecord(fmt.Sprintf("unit-%d.service", i%units))
		body["PRIORITY"] = fmt.Sprintf("%d", 3+i%4)
		if leftovers {
			maps.Copy(body, leftoverFields)
		}
		if err := sl.LogRecords().AppendEmpty().Body().SetEmptyMap().FromRaw(body); err != nil {
			b.Fatalf("set body: %v", err)
		}
	}
	return ld
}

func BenchmarkJournaldExtended(b *testing.B) {
	for _, mix := range []struct {
		name      string
		units     int
		leftovers bool
	}{{"single", 1, false}, {"mixed", 5, false}, {"leftovers", 5, true}} {
		template := journaldExtendedBatch(b, mix.units, mix.leftovers)
		b.Run(mix.name+"/impl=ottl", func(b *testing.B) {
			run(b, newLiveOTTL(b, consumertest.NewNop()), template)
		})
		b.Run(mix.name+"/impl=journald_extended", func(b *testing.B) {
			run(b, newReplacement(b, consumertest.NewNop()), template)
		})
	}
}

// Every record with leftover keys gains journald.unknown_fields, which the block never writes on a
// record with a MESSAGE. The test measures the bytes that adds to each record and logs them.
func TestJournaldExtendedParity_LeftoverKeysAddAnAttributeToRecordsWithMessage(t *testing.T) {
	ottlSink, procSink := new(consumertest.LogsSink), new(consumertest.LogsSink)
	ottl, replacement := newLiveOTTL(t, ottlSink), newReplacementWith(t, procSink, dropUnderscore)
	record := allFieldsRecord("x.service")
	maps.Copy(record, leftoverFields)

	outO := runThrough(t, ottl, ottlSink, bareBatch(t, nil, record))
	outP := runThrough(t, replacement, procSink, bareBatch(t, nil, record))

	_, lrO := singleRecord(t, outO)
	_, lrP := singleRecord(t, outP)
	if _, ok := lrO.Attributes().Get("journald.unknown_fields"); ok {
		t.Error("the live block wrote journald.unknown_fields on a record with MESSAGE")
	}
	unknown, ok := lrP.Attributes().Get("journald.unknown_fields")
	if !ok || !maps.Equal(unknown.Map().AsRaw(), leftoverFields) {
		t.Errorf("journald.unknown_fields = %v (present %v), want the four leftover keys", unknown.AsRaw(), ok)
	}
	var sizer plog.ProtoMarshaler
	sizeO, sizeP := sizer.LogsSize(outO), sizer.LogsSize(outP)
	if sizeP <= sizeO {
		t.Errorf("journaldprocessor output is %d bytes, the block's %d: want the extra attribute to show", sizeP, sizeO)
	}
	t.Logf("one record with %d leftover keys: block %d bytes, journaldprocessor %d bytes, +%d", len(leftoverFields), sizeO, sizeP, sizeP-sizeO)
}
