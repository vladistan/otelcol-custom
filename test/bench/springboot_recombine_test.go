// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package bench

import (
	"context"
	"testing"

	"go.opentelemetry.io/collector/component/componenttest"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/processor/processortest"

	"github.com/vladistan/otelcol-custom/processor/logrecombineprocessor"
	"github.com/vladistan/otelcol-custom/processor/logshapeprocessor"
)

// Generic names only: the layouts are those of the Spring Boot console format, the texts are made up.
const (
	sbHeader = "2026-11-22T14:38:13.666Z ERROR 7 --- [demo-app] [nio-8080-exec-1] c.e.demo.Controller : Request failed"
	sbAt     = "\tat com.example.demo.Controller.handle(Controller.java:10)"
	sbCaused = "Caused by: java.io.IOException: boom"
	sbMore   = "\t... 1 more"

	sbOlderHeader = "2026-11-22 14:38:13.666 ERROR 7 --- [main] c.e.demo.Service : Failed"
)

func journaldLine(line string) plog.Logs {
	ld := plog.NewLogs()
	lr := ld.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty().LogRecords().AppendEmpty()
	body := lr.Body().SetEmptyMap()
	body.PutStr("MESSAGE", line)
	body.PutStr("_PID", "7")
	return ld
}

// recombined sends the lines as separate journald records through logrecombine with its default
// settings and returns the records it forwards.
func recombined(t *testing.T, lines ...string) []plog.LogRecord {
	return recombinedWith(t, "", lines...)
}

// recombinedWith does the same with is_first_entry_pattern set to startPattern; empty keeps the default.
func recombinedWith(t *testing.T, startPattern string, lines ...string) []plog.LogRecord {
	t.Helper()
	sink := new(consumertest.LogsSink)
	factory := logrecombineprocessor.NewFactory()
	cfg := factory.CreateDefaultConfig().(*logrecombineprocessor.Config)
	if startPattern != "" {
		cfg.IsFirstEntryPattern = startPattern
	}
	proc, err := factory.CreateLogs(context.Background(), processortest.NewNopSettings(factory.Type()), cfg, sink)
	if err != nil {
		t.Fatalf("create logrecombine: %v", err)
	}
	if err := proc.Start(context.Background(), componenttest.NewNopHost()); err != nil {
		t.Fatalf("start: %v", err)
	}
	for _, line := range lines {
		if err := proc.ConsumeLogs(context.Background(), journaldLine(line)); err != nil {
			t.Fatalf("consume: %v", err)
		}
	}
	if err := proc.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	return recordsOfLogs(sink)
}

func recordsOfLogs(sink *consumertest.LogsSink) []plog.LogRecord {
	var out []plog.LogRecord
	for _, ld := range sink.AllLogs() {
		for i := 0; i < ld.ResourceLogs().Len(); i++ {
			for j := 0; j < ld.ResourceLogs().At(i).ScopeLogs().Len(); j++ {
				sl := ld.ResourceLogs().At(i).ScopeLogs().At(j)
				for k := 0; k < sl.LogRecords().Len(); k++ {
					out = append(out, sl.LogRecords().At(k))
				}
			}
		}
	}
	return out
}

// shapedText hands the text of a record to logshape as a plain body, as the journald step does,
// and returns what logshape forwards.
func shapedText(t *testing.T, text string) plog.LogRecord {
	t.Helper()
	sink := new(consumertest.LogsSink)
	factory := logshapeprocessor.NewFactory()
	proc, err := factory.CreateLogs(context.Background(), processortest.NewNopSettings(factory.Type()), factory.CreateDefaultConfig(), sink)
	if err != nil {
		t.Fatalf("create logshape: %v", err)
	}
	ld := plog.NewLogs()
	ld.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty().LogRecords().AppendEmpty().Body().SetStr(text)
	if err := proc.ConsumeLogs(context.Background(), ld); err != nil {
		t.Fatalf("logshape: %v", err)
	}
	recs := recordsOfLogs(sink)
	if len(recs) != 1 {
		t.Fatalf("logshape forwarded %d records, want 1", len(recs))
	}
	return recs[0]
}

func messageOf(t *testing.T, lr plog.LogRecord) string {
	t.Helper()
	v, ok := lr.Body().Map().Get("MESSAGE")
	if !ok {
		t.Fatalf("no MESSAGE in %v", lr.Body().AsRaw())
	}
	return v.Str()
}

func TestSpringBootTraceLeavesLogrecombineAndLogshapeAsOneRecord(t *testing.T) {
	recs := recombined(t, sbHeader, sbAt, sbCaused, sbMore)
	if len(recs) != 1 {
		t.Fatalf("logrecombine forwarded %d records, want 1", len(recs))
	}
	merged := messageOf(t, recs[0])

	lr := shapedText(t, merged)

	if got := lr.Body().Str(); got != "[c.e.demo.Controller] Request failed" {
		t.Fatalf("body = %q", got)
	}
	if lr.SeverityNumber() != plog.SeverityNumberError || lr.SeverityText() != "ERROR" {
		t.Fatalf("severity = %d %q", lr.SeverityNumber(), lr.SeverityText())
	}
	v, ok := lr.Attributes().Get("log.record.original")
	if !ok || v.Str() != sbHeader+"\n"+sbAt+"\n"+sbCaused+"\n"+sbMore {
		t.Fatalf("log.record.original = %v", v.AsRaw())
	}
}

func TestSpringBootTraceAsSeparateRecordsPassesLogshapeUnchanged(t *testing.T) {
	for name, line := range map[string]string{"at": sbAt, "caused by": sbCaused, "more": sbMore} {
		t.Run(name, func(t *testing.T) {
			lr := shapedText(t, line)

			if lr.Body().Str() != line || lr.SeverityNumber() != plog.SeverityNumberUnspecified || lr.Attributes().Len() != 0 {
				t.Fatalf("changed: body %q severity %d attrs %v", lr.Body().Str(), lr.SeverityNumber(), lr.Attributes().AsRaw())
			}
		})
	}
}

func TestSpringBootOlderFormIsNotAStartUnderTheDefaultRule(t *testing.T) {
	recs := recombined(t, sbHeader, sbOlderHeader)

	if len(recs) != 1 {
		t.Fatalf("logrecombine forwarded %d records, want 1: the default rule does not start an entry at a date with a space", len(recs))
	}
	if got := messageOf(t, recs[0]); got != sbHeader+"\n"+sbOlderHeader {
		t.Fatalf("MESSAGE = %q", got)
	}
}

func TestSpringBootOlderFormStartsAnEntryWithTheDocumentedPattern(t *testing.T) {
	recs := recombinedWith(t, `^\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}`, sbHeader, sbAt, sbOlderHeader)

	if len(recs) != 2 {
		t.Fatalf("logrecombine forwarded %d records, want 2", len(recs))
	}
	if got := messageOf(t, recs[0]); got != sbHeader+"\n"+sbAt {
		t.Fatalf("first MESSAGE = %q", got)
	}
	if got := messageOf(t, recs[1]); got != sbOlderHeader {
		t.Fatalf("second MESSAGE = %q", got)
	}
}
