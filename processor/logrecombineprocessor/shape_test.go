// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logrecombineprocessor

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/processor/processortest"
)

// The ANSI colour codes of the tests.
const (
	ansiRed   = "\x1b[31m"
	ansiGreen = "\x1b[32m"
	ansiReset = "\x1b[0m"
)

// recordSink keeps whole output records, so a test can read their severity and attributes.
type recordSink struct {
	mu   sync.Mutex
	recs []plog.LogRecord
}

func (*recordSink) Capabilities() consumer.Capabilities { return consumer.Capabilities{} }

func (s *recordSink) ConsumeLogs(_ context.Context, ld plog.Logs) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range ld.ResourceLogs().Len() {
		scopes := ld.ResourceLogs().At(i).ScopeLogs()
		for j := range scopes.Len() {
			records := scopes.At(j).LogRecords()
			for k := range records.Len() {
				c := plog.NewLogRecord()
				records.At(k).CopyTo(c)
				s.recs = append(s.recs, c)
			}
		}
	}
	return nil
}

func (s *recordSink) records() []plog.LogRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]plog.LogRecord(nil), s.recs...)
}

func startRecordProcessor(t *testing.T) (*logsProcessor, *recordSink) {
	t.Helper()
	sink := &recordSink{}
	p, err := newLogsProcessor(processortest.NewNopSettings(component.MustNewType(typeStr)), createDefaultConfig().(*Config), sink)
	require.NoError(t, err)
	require.NoError(t, p.Start(context.Background(), nil))
	t.Cleanup(func() { assert.NoError(t, p.Shutdown(context.Background())) })
	return p, sink
}

func message(rec plog.LogRecord) string {
	v, _ := rec.Body().Map().Get("MESSAGE")
	return v.Str()
}

func TestDaemonLogfmtLines_EachStartAnEntry(t *testing.T) {
	p, sink := startRecordProcessor(t)
	send(t, p, journalLog("", "", "900", `time="2026-11-20T01:00:00.1+01:00" level=info msg="first"`))
	send(t, p, journalLog("", "", "900", `time="2026-11-20T01:00:00.2+01:00" level=error msg="second"`))
	settle()

	got := sink.records()
	require.Len(t, got, 2, "each logfmt line keeps its own record, and with it its own priority")
	assert.Contains(t, message(got[0]), `msg="first"`)
	assert.Contains(t, message(got[1]), `msg="second"`)
}

func TestPythonTraceback_IsOneErrorRecordWithTheException(t *testing.T) {
	p, sink := startRecordProcessor(t)
	lines := []string{
		"Traceback (most recent call last):",
		`  File "app.py", line 3, in <module>`,
		"    run()",
		"ValueError: bad value 42",
	}
	for _, l := range lines {
		send(t, p, journalLog("bbbb", "py", "900", l))
	}
	settle()

	got := sink.records()
	require.Len(t, got, 1)
	assert.Equal(t, plog.SeverityNumberError, got[0].SeverityNumber())
	assert.Equal(t, "ERROR", got[0].SeverityText())
	a := got[0].Attributes()
	typ, _ := a.Get("exception.type")
	msg, _ := a.Get("exception.message")
	trace, _ := a.Get("exception.stacktrace")
	assert.Equal(t, "ValueError", typ.Str())
	assert.Equal(t, "bad value 42", msg.Str())
	assert.Equal(t, message(got[0]), trace.Str(), "the full text is the stack trace")
}

func TestTracebackAfterATimestampedLine_IsAlsoAnErrorRecord(t *testing.T) {
	p, sink := startRecordProcessor(t)
	send(t, p, journalLog("bbbb", "py", "900", "2026-11-20T01:00:00 ERROR job failed"))
	send(t, p, journalLog("bbbb", "py", "900", "Traceback (most recent call last):"))
	send(t, p, journalLog("bbbb", "py", "900", "KeyError: 'id'"))
	settle()

	got := sink.records()
	require.Len(t, got, 1)
	assert.Equal(t, plog.SeverityNumberError, got[0].SeverityNumber())
	typ, _ := got[0].Attributes().Get("exception.type")
	assert.Equal(t, "KeyError", typ.Str())
}

func TestTracebackThatNeverEnds_IsFlushedAsOneErrorRecord(t *testing.T) {
	p, sink := startRecordProcessor(t)
	send(t, p, journalLog("bbbb", "py", "900", "Traceback (most recent call last):"))
	send(t, p, journalLog("bbbb", "py", "900", `  File "app.py", line 3, in <module>`))
	settle()

	got := sink.records()
	require.Len(t, got, 1)
	assert.Equal(t, plog.SeverityNumberError, got[0].SeverityNumber())
	_, hasType := got[0].Attributes().Get("exception.type")
	assert.False(t, hasType, "no last line, so no exception type")
}

// Only Python and Java traces are marked as exceptions. A Go panic or a ClickHouse stack trace is
// joined by the gap rule like any burst of lines, and stays a record with no severity or exception.
func TestOtherTraceShapes_AreMergedByGapButNotMarkedAsExceptions(t *testing.T) {
	for name, lines := range map[string][]string{
		"go panic":   {"panic: boom", "goroutine 1 [running]:", "main.main()"},
		"clickhouse": {"2026.11.19 23:01:21.644520 [ 17 ] {} <Error> executeQuery: Code: 60. DB::Exception: Table missing", "0. DB::Exception::Exception() @ 0x1"},
	} {
		t.Run(name, func(t *testing.T) {
			p, sink := startRecordProcessor(t)
			for _, l := range lines {
				send(t, p, journalLog("cccc", "app", "900", l))
			}
			settle()

			got := sink.records()
			require.Len(t, got, 1)
			assert.Equal(t, plog.SeverityNumberUnspecified, got[0].SeverityNumber())
			_, has := got[0].Attributes().Get("exception.type")
			assert.False(t, has)
		})
	}
}

func TestPlainRecord_KeepsItsSeverityAndGetsNoException(t *testing.T) {
	p, sink := startRecordProcessor(t)
	send(t, p, journalLog("bbbb", "web", "900", "2026-11-20T01:00:00 INFO served"))
	settle()

	got := sink.records()
	require.Len(t, got, 1)
	assert.Equal(t, plog.SeverityNumberUnspecified, got[0].SeverityNumber())
	_, has := got[0].Attributes().Get("exception.type")
	assert.False(t, has)
}

func TestAnsiCodes_AreStrippedFromTheMessage(t *testing.T) {
	p, sink := startRecordProcessor(t)
	send(t, p, journalLog("bbbb", "web", "900", "2026-11-20T01:00:00 "+ansiRed+"red"+ansiReset+" alert"))
	settle()

	got := sink.records()
	require.Len(t, got, 1)
	assert.Equal(t, "2026-11-20T01:00:00 red alert", message(got[0]))
}

func TestBlankRecords_AreDropped(t *testing.T) {
	for _, blank := range []string{"   ", "\n\n", "\x1b[0m", "\r\n", `\r\n`, `\x1b[0m\r\n`} {
		p, sink := startRecordProcessor(t)
		send(t, p, journalLog("bbbb", "web", "900", blank))
		settle()

		assert.Empty(t, sink.records(), "blank %q", blank)
	}
}

func TestPlainStringBody_HasItsAnsiStrippedAndBlankDropped(t *testing.T) {
	p, sink := startRecordProcessor(t)
	for _, text := range []string{ansiGreen + "green" + ansiReset, "   "} {
		ld := plog.NewLogs()
		ld.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty().LogRecords().AppendEmpty().Body().SetStr(text)
		send(t, p, ld)
	}
	settle()

	got := sink.records()
	require.Len(t, got, 1)
	assert.Equal(t, "green", got[0].Body().Str())
}

func TestShapeOptions_OffKeepTheRecordsAsTheyWere(t *testing.T) {
	sink := &recordSink{}
	cfg := createDefaultConfig().(*Config)
	cfg.StripANSI, cfg.DropBlank = false, false
	p, err := newLogsProcessor(processortest.NewNopSettings(component.MustNewType(typeStr)), cfg, sink)
	require.NoError(t, err)
	require.NoError(t, p.Start(context.Background(), nil))
	t.Cleanup(func() { assert.NoError(t, p.Shutdown(context.Background())) })

	send(t, p, journalLog("bbbb", "web", "900", ansiRed+"red"))
	send(t, p, journalLog("cccc", "web2", "901", "  "))
	settle()

	got := sink.records()
	require.Len(t, got, 2)
}
