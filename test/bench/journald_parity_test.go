// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package bench

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/open-telemetry/opentelemetry-collector-contrib/processor/transformprocessor"
	"go.opentelemetry.io/collector/confmap"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/processor"
	"go.opentelemetry.io/collector/processor/processortest"
	"gopkg.in/yaml.v3"

	"github.com/vladistan/otelcol-custom/processor/journaldprocessor"
)

const (
	gatewayConfig   = "../configs/local-gateway.yaml"
	journaldFixture = "../../processor/journaldprocessor/testdata/records.json"
	otelMainText    = "testdata/otel-main-transform-journald.yaml"
	ottlBlock       = "transform/journald"
)

var resourceKeys = []string{"host.name", "host.id", "process.pid", "process.command", "service.name"}

func loadRecords(t *testing.T) []map[string]any {
	t.Helper()
	raw, err := os.ReadFile(journaldFixture)
	if err != nil {
		t.Fatalf("read fixtures: %v", err)
	}
	var records []map[string]any
	if err := json.Unmarshal(raw, &records); err != nil {
		t.Fatalf("parse fixtures: %v", err)
	}
	return records
}

func newOTTL(t testing.TB, sink consumer.Logs) processor.Logs {
	t.Helper()
	raw, err := os.ReadFile(gatewayConfig)
	if err != nil {
		t.Fatalf("read gateway config: %v", err)
	}
	var doc struct {
		Processors map[string]map[string]any `yaml:"processors"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse gateway config: %v", err)
	}
	block, ok := doc.Processors[ottlBlock]
	if !ok {
		t.Fatalf("%s not found in %s", ottlBlock, gatewayConfig)
	}
	return newOTTLFromBlock(t, sink, block)
}

// newOTTLOtelMain builds the block from the text delivered for otel-main.
func newOTTLOtelMain(t testing.TB, sink consumer.Logs) processor.Logs {
	t.Helper()
	raw, err := os.ReadFile(otelMainText)
	if err != nil {
		t.Fatalf("read otel-main text: %v", err)
	}
	var doc map[string]map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse otel-main text: %v", err)
	}
	block, ok := doc[ottlBlock]
	if !ok {
		t.Fatalf("%s not found in %s", ottlBlock, otelMainText)
	}
	return newOTTLFromBlock(t, sink, block)
}

func newOTTLFromBlock(t testing.TB, sink consumer.Logs, block map[string]any) processor.Logs {
	t.Helper()
	factory := transformprocessor.NewFactory()
	cfg := factory.CreateDefaultConfig()
	if err := confmap.NewFromStringMap(block).Unmarshal(cfg); err != nil {
		t.Fatalf("unmarshal %s: %v", ottlBlock, err)
	}
	proc, err := factory.CreateLogs(context.Background(), processortest.NewNopSettings(factory.Type()), cfg, sink)
	if err != nil {
		t.Fatalf("create transformprocessor: %v", err)
	}
	return proc
}

func newJournald(t testing.TB, sink consumer.Logs) processor.Logs {
	t.Helper()
	factory := journaldprocessor.NewFactory()
	proc, err := factory.CreateLogs(context.Background(), processortest.NewNopSettings(factory.Type()), factory.CreateDefaultConfig(), sink)
	if err != nil {
		t.Fatalf("create journaldprocessor: %v", err)
	}
	return proc
}

func batchOf(t *testing.T, records ...map[string]any) plog.Logs {
	t.Helper()
	ld := plog.NewLogs()
	sl := ld.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty()
	for _, body := range records {
		if err := sl.LogRecords().AppendEmpty().Body().SetEmptyMap().FromRaw(body); err != nil {
			t.Fatalf("set body: %v", err)
		}
	}
	return ld
}

func runThrough(t *testing.T, proc processor.Logs, sink *consumertest.LogsSink, ld plog.Logs) plog.Logs {
	t.Helper()
	sink.Reset()
	if err := proc.ConsumeLogs(context.Background(), ld); err != nil {
		t.Fatalf("consume: %v", err)
	}
	if len(sink.AllLogs()) != 1 {
		t.Fatalf("sink got %d batches, want 1", len(sink.AllLogs()))
	}
	return sink.AllLogs()[0]
}

type setFields struct {
	resource map[string]any
	number   plog.SeverityNumber
	text     string
}

// fieldsSetByOTTL reads only what the transform/journald block sets.
func fieldsSetByOTTL(res pcommon.Map, lr plog.LogRecord) setFields {
	out := setFields{resource: map[string]any{}, number: lr.SeverityNumber(), text: lr.SeverityText()}
	for _, key := range resourceKeys {
		if v, ok := res.Get(key); ok {
			out.resource[key] = v.AsRaw()
		}
	}
	return out
}

func singleRecord(t *testing.T, ld plog.Logs) (pcommon.Map, plog.LogRecord) {
	t.Helper()
	if ld.ResourceLogs().Len() != 1 || ld.LogRecordCount() != 1 {
		t.Fatalf("got %d ResourceLogs and %d records, want 1 and 1", ld.ResourceLogs().Len(), ld.LogRecordCount())
	}
	rl := ld.ResourceLogs().At(0)
	return rl.Resource().Attributes(), rl.ScopeLogs().At(0).LogRecords().At(0)
}

func TestJournaldParity_EachRecordMatchesOTTLOnFieldsItSets(t *testing.T) {
	ottlSink, journaldSink := new(consumertest.LogsSink), new(consumertest.LogsSink)
	ottl, journald := newOTTL(t, ottlSink), newJournald(t, journaldSink)

	for i, record := range loadRecords(t) {
		resOTTL, lrOTTL := singleRecord(t, runThrough(t, ottl, ottlSink, batchOf(t, record)))
		resJournald, lrJournald := singleRecord(t, runThrough(t, journald, journaldSink, batchOf(t, record)))

		want := fieldsSetByOTTL(resOTTL, lrOTTL)
		got := fieldsSetByOTTL(resJournald, lrJournald)

		if got.number != want.number || got.text != want.text {
			t.Errorf("record %d severity = %v/%q, OTTL gave %v/%q", i, got.number, got.text, want.number, want.text)
		}
		if len(got.resource) != len(want.resource) {
			t.Errorf("record %d resource = %v, OTTL gave %v", i, got.resource, want.resource)
		}
		for key, value := range want.resource {
			if got.resource[key] != value {
				t.Errorf("record %d %s = %v, OTTL gave %v", i, key, got.resource[key], value)
			}
		}
	}
}

func TestJournaldParity_MixedUnits_OnlyD5Differs(t *testing.T) {
	ottlSink, journaldSink := new(consumertest.LogsSink), new(consumertest.LogsSink)
	ottl, journald := newOTTL(t, ottlSink), newJournald(t, journaldSink)
	records := loadRecords(t)[:3]

	outOTTL := runThrough(t, ottl, ottlSink, batchOf(t, records...))
	outJournald := runThrough(t, journald, journaldSink, batchOf(t, records...))

	if outOTTL.ResourceLogs().Len() != 1 {
		t.Fatalf("OTTL produced %d ResourceLogs, want 1 (the OTTL block keeps one shared resource; journaldprocessor splits by resource)", outOTTL.ResourceLogs().Len())
	}
	if outJournald.ResourceLogs().Len() != len(records) {
		t.Fatalf("journald produced %d ResourceLogs, want %d (split by resource)", outJournald.ResourceLogs().Len(), len(records))
	}
	if outJournald.LogRecordCount() != outOTTL.LogRecordCount() {
		t.Errorf("record count %d, OTTL gave %d", outJournald.LogRecordCount(), outOTTL.LogRecordCount())
	}
	for i := 0; i < outJournald.ResourceLogs().Len(); i++ {
		rl := outJournald.ResourceLogs().At(i)
		lr := rl.ScopeLogs().At(0).LogRecords().At(0)
		want := fieldsSetByOTTL(singleResourceOf(t, records, lr.Body().AsString(), ottl, ottlSink))
		got := fieldsSetByOTTL(rl.Resource().Attributes(), lr)
		if got.number != want.number || got.text != want.text {
			t.Errorf("split record %d severity = %v/%q, per-record OTTL gave %v/%q", i, got.number, got.text, want.number, want.text)
		}
		for key, value := range want.resource {
			if got.resource[key] != value {
				t.Errorf("split record %d %s = %v, per-record OTTL gave %v", i, key, got.resource[key], value)
			}
		}
	}
}

func TestJournaldParity_OtelMainText_EachPriorityMatches(t *testing.T) {
	ottlSink, journaldSink := new(consumertest.LogsSink), new(consumertest.LogsSink)
	ottl, journald := newOTTLOtelMain(t, ottlSink), newJournald(t, journaldSink)

	for _, priority := range []string{"0", "1", "2", "3", "4", "5", "6", "7"} {
		record := map[string]any{"_HOSTNAME": "host-a.example.com", "PRIORITY": priority, "MESSAGE": "m"}

		_, lrOTTL := singleRecord(t, runThrough(t, ottl, ottlSink, batchOf(t, record)))
		_, lrJournald := singleRecord(t, runThrough(t, journald, journaldSink, batchOf(t, record)))

		if priority == "5" {
			// PRIORITY 5: the otel-main block has no rule for it, while journaldprocessor maps it to INFO. The difference is intended.
			if lrOTTL.SeverityNumber() != plog.SeverityNumberUnspecified || lrOTTL.SeverityText() != "" {
				t.Errorf("PRIORITY 5: otel-main OTTL gave %v/%q, expected the block to leave it unset",
					lrOTTL.SeverityNumber(), lrOTTL.SeverityText())
			}
			if lrJournald.SeverityNumber() != plog.SeverityNumberInfo || lrJournald.SeverityText() != "INFO" {
				t.Errorf("PRIORITY 5: journald gave %v/%q, want INFO", lrJournald.SeverityNumber(), lrJournald.SeverityText())
			}
			continue
		}
		if lrJournald.SeverityNumber() != lrOTTL.SeverityNumber() || lrJournald.SeverityText() != lrOTTL.SeverityText() {
			t.Errorf("PRIORITY %s: journald %v/%q, otel-main OTTL %v/%q", priority,
				lrJournald.SeverityNumber(), lrJournald.SeverityText(), lrOTTL.SeverityNumber(), lrOTTL.SeverityText())
		}
	}
}

// singleResourceOf runs the OTTL block on the one fixture whose MESSAGE equals message.
func singleResourceOf(t *testing.T, records []map[string]any, message string, ottl processor.Logs, sink *consumertest.LogsSink) (pcommon.Map, plog.LogRecord) {
	t.Helper()
	for _, record := range records {
		if record["MESSAGE"] == message {
			return singleRecord(t, runThrough(t, ottl, sink, batchOf(t, record)))
		}
	}
	t.Fatalf("no fixture with MESSAGE %q", message)
	return pcommon.Map{}, plog.LogRecord{}
}
