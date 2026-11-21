// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package multiline

import (
	"strings"
	"testing"
)

const pyTrace = `Traceback (most recent call last):
  File "/app/bridge.py", line 88, in send
    resp = session.post(url)
urllib3.exceptions.ConnectTimeoutError: Connection to otel.example.org timed out
requests.exceptions.ConnectTimeout: HTTPConnectionPool(host='otel.example.org', port=4318): Max retries exceeded with url: /v1/metrics`

func TestIsContinuation_TracebackLinesAreContinuations(t *testing.T) {
	for _, c := range []struct {
		line string
		want bool
	}{
		{`  File "/app/bridge.py", line 88, in send`, true},
		{"    resp = session.post(url)", true},
		{"\tat com.example.Main(Main.java:10)", true},
		{"Caused by: java.io.IOException: boom", true},
		{"... 12 more", true},
		{"During handling of the above exception, another exception occurred:", true},
		{"The above exception was the direct cause of the following exception:", true},
		{"Traceback (most recent call last):", false},
		{"2026-11-20 00:27:56 [info] started", false},
		{"", false},
	} {
		if got := IsContinuation(c.line); got != c.want {
			t.Errorf("IsContinuation(%q) = %v, want %v", c.line, got, c.want)
		}
	}
}

func TestIsStart_TracebackHeaderStartsARecord(t *testing.T) {
	if !IsStart("Traceback (most recent call last):") {
		t.Fatal("traceback header must start a record")
	}
	if IsStart("    resp = session.post(url)") {
		t.Fatal("an indented line does not start a record")
	}
}

func TestMerger_FoldsATracebackIntoOneRecord(t *testing.T) {
	var m Merger
	var out []string
	for _, line := range strings.Split(pyTrace, "\n") {
		if rec, ok := m.Add(line); ok {
			out = append(out, rec)
		}
	}
	if rec, ok := m.Flush(); ok {
		out = append(out, rec)
	}
	if len(out) != 1 || out[0] != pyTrace {
		t.Fatalf("got %d records: %q", len(out), out)
	}
}

func TestMerger_PlainLinesPassOneByOne(t *testing.T) {
	var m Merger
	var out []string
	for _, line := range []string{"first", "second", "third"} {
		if rec, ok := m.Add(line); ok {
			out = append(out, rec)
		}
	}
	if rec, ok := m.Flush(); ok {
		out = append(out, rec)
	}
	if strings.Join(out, "|") != "first|second|third" {
		t.Fatalf("got %q", out)
	}
}

func TestMerger_NewRecordAfterATraceback_ClosesTheTraceback(t *testing.T) {
	var m Merger
	var out []string
	for _, line := range []string{"Traceback (most recent call last):", `  File "a.py", line 1, in f`, "ValueError: x", "next line"} {
		if rec, ok := m.Add(line); ok {
			out = append(out, rec)
		}
	}
	if rec, ok := m.Flush(); ok {
		out = append(out, rec)
	}
	if len(out) != 2 || !strings.HasSuffix(out[0], "ValueError: x") || out[1] != "next line" {
		t.Fatalf("got %q", out)
	}
}

func TestMerger_BlankLinesAreDropped(t *testing.T) {
	var m Merger
	if _, ok := m.Add("   "); ok {
		t.Fatal("a blank line must not make a record")
	}
	if _, ok := m.Flush(); ok {
		t.Fatal("nothing pending after a blank line")
	}
}

func TestMerger_CapsTheRecordSize(t *testing.T) {
	m := Merger{MaxBytes: 64}
	var out []string
	if rec, ok := m.Add("Traceback (most recent call last):"); ok {
		out = append(out, rec)
	}
	for range 20 {
		if rec, ok := m.Add("  File \"x.py\", line 1, in f"); ok {
			out = append(out, rec)
		}
	}
	if rec, ok := m.Flush(); ok {
		out = append(out, rec)
	}
	if len(out) < 2 {
		t.Fatalf("the size cap must split the record, got %d", len(out))
	}
	for _, r := range out {
		if len(r) > 64+len("  File \"x.py\", line 1, in f")+1 {
			t.Fatalf("record far over the cap: %d bytes", len(r))
		}
	}
}

func TestSplitTraceback_LastLineGivesTypeAndMessage(t *testing.T) {
	typ, msg, ok := SplitException(pyTrace)
	if !ok || typ != "requests.exceptions.ConnectTimeout" || !strings.HasPrefix(msg, "HTTPConnectionPool(host='otel.example.org'") {
		t.Fatalf("got %q %q %v", typ, msg, ok)
	}
}

func TestSplitException_NoColon_GivesTypeOnly(t *testing.T) {
	typ, msg, ok := SplitException("Traceback (most recent call last):\n  File \"a.py\", line 1, in f\nKeyboardInterrupt")
	if !ok || typ != "KeyboardInterrupt" || msg != "" {
		t.Fatalf("got %q %q %v", typ, msg, ok)
	}
}

func TestSplitException_NotATraceback_IsNotOK(t *testing.T) {
	if _, _, ok := SplitException("just a line"); ok {
		t.Fatal("a plain line has no exception")
	}
}

func TestJoinLines_KeepsEveryLineOfAJournaldBatch(t *testing.T) {
	const nl = "\n"
	got := JoinLines([]string{"wrapper | a", "wrapper | b", "wrapper | c"})
	if got != "wrapper | a"+nl+"wrapper | b"+nl+"wrapper | c" {
		t.Fatalf("got %q", got)
	}
}
