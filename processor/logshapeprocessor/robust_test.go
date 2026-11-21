// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"fmt"
	"strings"
	"testing"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
)

// oddInputs are lines a parser must survive: short, of every length around the cut points the parsers
// use, made of separators only, cut in the middle of a known format, or not valid UTF-8.
func oddInputs(tb testing.TB) []string {
	tb.Helper()
	odd := []string{
		"", " ", "x", "\n", "\t", "\x00", "\xff", "\xff\xfe\xfd\xfc", "\xc3\x28", "é", strings.Repeat("\xff", 40),
		strings.Repeat("9", 14), strings.Repeat("9", 15), strings.Repeat("9", 16), strings.Repeat("9", 29), strings.Repeat("9", 30), strings.Repeat("9", 31),
		strings.Repeat(":", 15), strings.Repeat(":", 29), strings.Repeat(":", 30), strings.Repeat("[", 15), strings.Repeat("[", 29), strings.Repeat("[", 30),
		strings.Repeat("]", 30), strings.Repeat("{", 30), strings.Repeat("\"", 30), strings.Repeat("%", 30), strings.Repeat("<", 30), strings.Repeat("-", 30),
		strings.Repeat("0:", 15), strings.Repeat("[:", 15), strings.Repeat("9:9 ", 8), strings.Repeat("\t", 31),
		"12:34:56", "12:34:56 [", "12:34:56 [INFO", "12:34:56 [INFO]", "12:34:56 [INFO] a", "12:34:56 [INFO] a:", "23:01:22 - done: all good",
		"I1119", "I1119 23:05:39.18", "I1119 23:05:39.184859       1 a.go:1]", "I1119 23:05:39.184859       1 a.go:1] \"", "I1339 25:61:00.000000       1 a.go:1] \"m\"",
		"INFO:", "INFO:     ", "INFO:     1.2.3.4:1 - \"", "WARNING:  x - \"GET / HTTP/1.1\" 99999999999999999999",
		`{"level"`, `{"level":"info"`, `{"level":"info","ts":`, `{"level":"info","ts":1e999,"msg":"x"}`, `{"level":"info","ts":-1,"msg":"x"}`,
		`{"level":"info","msg":"x","error":{"type":1,"message":[]}}`, `{"level":"info","msg":"x","caller":":"}`, `{"level":"info","msg":"x","caller":":::"}`,
		`time="" level= msg="`, `time="x" level=info msg="`, "2026-01-02T14:18:43.362Z\tinfo\t\t\t", "2026-01-02T14:18:43.362Z\tinfo\ta\tb",
		"2026-11-19 22:42:20.918 UTC [", "2026-11-19 22:42:20.918 UTC [1] LOG:", "2026-11-19 22:42:20.918 UTC [99999999999999999999] LOG:  x",
		"1:M", "1:M 19 Nov", "1:M 19 Nov 2026 23:22:37.151 *", "1:M 19 Nov 2026 23:22:37.151 * ", "99999999999999999999:M 1 Jan 2026 00:00:00.000 * x",
		"1.2.3.4 - - [", "1.2.3.4 - - [20/Nov/2026:00:26:38 +0000] \"", "1.2.3.4 - - [20/Nov/2026:00:26:38 +0000] \"GET\" 200 1 \"-\" \"-\"",
		"%", "%A-1-B:", "%A-1-B: ", " Dec 15 22:56:14 h A[b]: c.c(1) 2 %%", " Dec 15 22:56:14 h A[b]: c.c(99999999999999999999) 2 %%", "a: *b: Dec 1 00:00:00.000: %C-5-D:",
		"<", "<1", "<13>", "<13>Dec", "Dec 31 10:05:00", "Dec 31 10:05:00 a", "Dec 31 10:05:00 a:", "Dec 31 10:05:00 a b[", "1 2026-01-02T00:00:00Z h a 1 - -",
	}
	for _, lines := range loadSamples(tb) {
		for _, line := range lines {
			odd = append(odd, line)
			for i := range len(line) {
				odd = append(odd, line[:i], line[i:])
			}
		}
	}
	return odd
}

// recordsFor builds the shapes a branch may meet: the text in the body, in a map body, in the source
// attributes the branches read, and a body of another type.
func recordsFor(text string) map[string]plog.LogRecord {
	out := map[string]plog.LogRecord{}
	body := plog.NewLogRecord()
	body.Body().SetStr(text)
	out["body"] = body

	mapBody := plog.NewLogRecord()
	mapBody.Body().SetEmptyMap().PutStr("text", text)
	out["map body"] = mapBody

	attrs := plog.NewLogRecord()
	for _, k := range []string{"message", "full_message", "log.record.original", "original_message", "priority", "cisco_facility", "net.peer.name"} {
		attrs.Attributes().PutStr(k, text)
	}
	out["attributes"] = attrs

	bytes := plog.NewLogRecord()
	bytes.Body().SetEmptyBytes().FromRaw([]byte(text))
	out["bytes body"] = bytes

	number := plog.NewLogRecord()
	number.Body().SetInt(int64(len(text)))
	number.Attributes().PutInt("flow.src_as", int64(len(text)))
	number.Attributes().PutStr("flow.dst_as", text)
	out["number body"] = number
	return out
}

func TestRobust_NoBranchPanicsOnOddInput(t *testing.T) {
	branches, err := newBranches(defaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	inputs := oddInputs(t)
	t.Logf("%d odd inputs, %d branches", len(inputs), len(branches))
	for _, b := range branches {
		panics := 0
		for _, text := range inputs {
			for shape, lr := range recordsFor(text) {
				res, scope := pcommon.NewResource(), pcommon.NewInstrumentationScope()
				r := &rec{lr: lr, res: res, scope: scope, body: bodyText(lr)}
				if msg := panicOf(func() {
					if b.claims(r) {
						_ = b.parse(r)
					}
					_ = b.parse(r) // parse must also refuse what claims did not take, without a panic
				}); msg != "" {
					panics++
					if panics <= 3 {
						t.Errorf("branch %s panics on %s %q: %s", b.name(), shape, shorten(text), msg)
					}
				}
			}
		}
		if panics > 3 {
			t.Errorf("branch %s: %d panics in all", b.name(), panics)
		}
	}
}

func panicOf(f func()) (msg string) {
	defer func() {
		if r := recover(); r != nil {
			msg = fmt.Sprint(r)
		}
	}()
	f()
	return ""
}

func shorten(s string) string {
	if len(s) > 60 {
		return s[:60] + "..."
	}
	return s
}
