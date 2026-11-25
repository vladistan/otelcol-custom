// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package bench

import (
	"context"
	"testing"

	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/processor/processortest"

	"github.com/vladistan/otelcol-custom/processor/logshapeprocessor"
)

// Generic names only: the layout is the Python logging default, the texts are made up.
const (
	pyHeader      = "WARNING:app:Request failed"
	pyOtherHeader = "INFO:app:Next request"
	pyStartRule   = `^(DEBUG|INFO|WARNING|WARN|ERROR|CRITICAL|FATAL):[A-Za-z0-9_.]+:`
)

var pyTraceLines = []string{
	"Traceback (most recent call last):",
	`  File "app.py", line 1, in handle`,
	"ValueError: boom",
}

func withTrace(header string) []string { return append([]string{header}, pyTraceLines...) }

// shapedRecord hands a record that left logrecombine to logshape the way the journald step does: the body
// becomes the text of MESSAGE and the severity and attributes stay as logrecombine left them.
func shapedRecord(t *testing.T, merged plog.LogRecord) plog.LogRecord {
	t.Helper()
	sink := new(consumertest.LogsSink)
	factory := logshapeprocessor.NewFactory()
	proc, err := factory.CreateLogs(context.Background(), processortest.NewNopSettings(factory.Type()), factory.CreateDefaultConfig(), sink)
	if err != nil {
		t.Fatalf("create logshape: %v", err)
	}
	ld := plog.NewLogs()
	lr := ld.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty().LogRecords().AppendEmpty()
	merged.CopyTo(lr)
	lr.Body().SetStr(messageOf(t, merged))
	if err := proc.ConsumeLogs(context.Background(), ld); err != nil {
		t.Fatalf("logshape: %v", err)
	}
	recs := recordsOfLogs(sink)
	if len(recs) != 1 {
		t.Fatalf("logshape forwarded %d records, want 1", len(recs))
	}
	return recs[0]
}

func TestPyLoggerTracebackLeavesLogrecombineAndLogshapeAsOneRecord(t *testing.T) {
	recs := recombined(t, withTrace(pyHeader)...)
	if len(recs) != 1 {
		t.Fatalf("logrecombine forwarded %d records, want 1", len(recs))
	}

	lr := shapedRecord(t, recs[0])

	if got := lr.Body().Str(); got != "[app] Request failed" {
		t.Fatalf("body = %q", got)
	}
	v, ok := lr.Attributes().Get("log.record.original")
	if !ok || v.Str() != pyHeader+"\n"+pyTraceLines[0]+"\n"+pyTraceLines[1]+"\n"+pyTraceLines[2] {
		t.Fatalf("log.record.original = %v", v.AsRaw())
	}
}

func TestPyLoggerLogrecombineMarksTheTracebackErrorAndTheLevelOfTheHeaderReplacesIt(t *testing.T) {
	recs := recombined(t, withTrace(pyHeader)...)
	if len(recs) != 1 {
		t.Fatalf("logrecombine forwarded %d records, want 1", len(recs))
	}
	if recs[0].SeverityNumber() != plog.SeverityNumberError {
		t.Fatalf("logrecombine severity = %d, want ERROR", recs[0].SeverityNumber())
	}

	lr := shapedRecord(t, recs[0])

	if lr.SeverityNumber() != plog.SeverityNumberWarn || lr.SeverityText() != "WARN" {
		t.Fatalf("severity = %d %q, want WARN from the header", lr.SeverityNumber(), lr.SeverityText())
	}
}

func TestPyLoggerTracebackAsSeparateRecordsPassesLogshapeUnchanged(t *testing.T) {
	for _, line := range pyTraceLines {
		t.Run(line, func(t *testing.T) {
			lr := shapedText(t, line)

			if lr.Body().Str() != line || lr.SeverityNumber() != plog.SeverityNumberUnspecified || lr.Attributes().Len() != 0 {
				t.Fatalf("changed: body %q severity %d attrs %v", lr.Body().Str(), lr.SeverityNumber(), lr.Attributes().AsRaw())
			}
		})
	}
}

func TestPyLoggerDefaultRuleMergesTwoLoggingLinesThatArriveTogether(t *testing.T) {
	recs := recombined(t, pyHeader, pyOtherHeader)

	if len(recs) != 1 {
		t.Fatalf("logrecombine forwarded %d records, want 1: the default rule starts an entry only at an ISO date", len(recs))
	}
}

func TestPyLoggerDocumentedPatternStartsAnEntryAtEachLoggingLine(t *testing.T) {
	recs := recombinedWith(t, pyStartRule, append(withTrace(pyHeader), pyOtherHeader)...)

	if len(recs) != 2 {
		t.Fatalf("logrecombine forwarded %d records, want 2", len(recs))
	}
	if got := messageOf(t, recs[1]); got != pyOtherHeader {
		t.Fatalf("second MESSAGE = %q", got)
	}
}
