// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

// cspell:ignore vstan

package logshapeprocessor

import (
	"testing"
	"time"

	"go.opentelemetry.io/collector/pdata/plog"
)

// Invented fixture: the shape of a pino line with a tag prefix, not a capture.
const pinoError = `[JOB-RUNNER] {"level":50,"time":1795129202193,"pid":19,"hostname":"app-5d4f7","trace_id":"4bf92f3577b34da6a3ce929d0e0e4736","error":{"name":"TypeError","message":"fetch failed","stack":"TypeError: fetch failed\n    at node:internal/deps/undici"}}`

func TestPino_TaggedErrorLine_GivesTheErrorMessageTheScopeAndTheTrace(t *testing.T) {
	h := newHarness(t, defaultConfig())

	lr, scope := h.runScoped(input{body: pinoError})

	wantBody(t, lr, "fetch failed")
	wantSeverity(t, lr, plog.SeverityNumberError, "ERROR")
	wantTime(t, lr, time.Date(2026, 11, 19, 23, 0, 2, 193000000, time.UTC))
	wantScope(t, scope, "JOB-RUNNER")
	if got := lr.TraceID().String(); got != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Fatalf("trace id = %q", got)
	}
	a := lr.Attributes()
	attrInt(t, a, "process.pid", 19)
	if attrStr(t, a, "exception.type") != "TypeError" || attrStr(t, a, "exception.message") != "fetch failed" {
		t.Fatalf("attributes = %v", a.AsRaw())
	}
	if attrStr(t, a, "exception.stacktrace") != "TypeError: fetch failed\n    at node:internal/deps/undici" || attrStr(t, a, "log.record.original") != pinoError {
		t.Fatalf("attributes = %v", a.AsRaw())
	}
	absent(t, a, "hostname", "pid", "error", "level", "time", "trace_id")
}

func TestPino_UntaggedLineWithAMessage_GivesTheMessage(t *testing.T) {
	h := newHarness(t, defaultConfig())

	lr, scope := h.runScoped(input{body: `{"level":30,"time":1795129202193,"pid":7,"hostname":"app-5d4f7","msg":"request completed","statusCode":200}`})

	wantBody(t, lr, "request completed")
	wantSeverity(t, lr, plog.SeverityNumberInfo, "INFO")
	wantScope(t, scope, "")
	attrInt(t, lr.Attributes(), "vstan.pino.statusCode", 200)
	absent(t, lr.Attributes(), "statusCode")
}

func TestPino_EveryLevel_MapsOnePinoNumber(t *testing.T) {
	want := map[string]plog.SeverityNumber{
		"10": plog.SeverityNumberTrace, "20": plog.SeverityNumberDebug, "30": plog.SeverityNumberInfo,
		"40": plog.SeverityNumberWarn, "50": plog.SeverityNumberError, "60": plog.SeverityNumberFatal,
	}
	for level, num := range want {
		h := newHarness(t, defaultConfig())
		lr, _ := h.runScoped(input{body: `{"level":` + level + `,"time":1795129202193,"msg":"m"}`})
		if lr.SeverityNumber() != num {
			t.Fatalf("level %s -> %d, want %d", level, lr.SeverityNumber(), num)
		}
	}
}

func TestPino_UnknownLevelNumber_LeavesTheSeverityUnset(t *testing.T) {
	h := newHarness(t, defaultConfig())

	lr, _ := h.runScoped(input{body: `{"level":35,"time":1795129202193,"msg":"m"}`})

	wantBody(t, lr, "m")
	wantSeverity(t, lr, plog.SeverityNumberUnspecified, "")
}

func TestPino_ErrorObjectWithMoreFields_KeepsThemUnderTheBranchNamespace(t *testing.T) {
	h := newHarness(t, defaultConfig())

	lr, _ := h.runScoped(input{body: `{"level":50,"time":1795129202193,"msg":"failed","error":{"name":"Error","message":"boom","code":"ECONNRESET","statusCode":502}}`})

	a := lr.Attributes()
	if attrStr(t, a, "exception.type") != "Error" || attrStr(t, a, "vstan.pino.error.code") != "ECONNRESET" {
		t.Fatalf("attributes = %v", a.AsRaw())
	}
	attrInt(t, a, "vstan.pino.error.statusCode", 502)
	absent(t, a, "error.code", "error.statusCode")
}

func TestPino_ErrorThatIsAPlainString_IsTheExceptionMessage(t *testing.T) {
	h := newHarness(t, defaultConfig())

	lr, _ := h.runScoped(input{body: `{"level":50,"time":1795129202193,"msg":"failed","error":"connection reset"}`})

	if attrStr(t, lr.Attributes(), "exception.message") != "connection reset" {
		t.Fatalf("attributes = %v", lr.Attributes().AsRaw())
	}
	absent(t, lr.Attributes(), "error")
}

func TestPino_PidThatIsNotAnInteger_IsUnchangedAndCounted(t *testing.T) {
	h := newHarness(t, defaultConfig())
	const bad = `{"level":30,"time":1795129202193,"msg":"m","pid":"seven"}`

	lr, _ := h.runScoped(input{body: bad})

	wantUnchanged(t, lr, bad, map[string]any{})
	wantFailed(t, h, "pino")
}

func TestPino_TimeOutsideTheRange_IsUnchangedAndCounted(t *testing.T) {
	for _, bad := range []string{`-1`, `1e400`, `99999999999999999999`, `10`} {
		h := newHarness(t, defaultConfig())
		body := `{"level":30,"time":` + bad + `,"msg":"m"}`

		lr, _ := h.runScoped(input{body: body})

		wantUnchanged(t, lr, body, map[string]any{})
		wantFailed(t, h, "pino")
	}
}

func TestPino_BadTraceID_IsUnchangedAndCounted(t *testing.T) {
	h := newHarness(t, defaultConfig())
	const bad = `{"level":50,"time":1795129202193,"msg":"m","trace_id":"not-hex"}`

	lr, _ := h.runScoped(input{body: bad})

	wantUnchanged(t, lr, bad, map[string]any{})
	wantFailed(t, h, "pino")
}
