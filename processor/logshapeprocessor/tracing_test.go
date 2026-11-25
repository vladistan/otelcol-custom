// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"testing"
	"time"

	"go.opentelemetry.io/collector/pdata/plog"
)

// Invented fixture: the shape of a Rust tracing JSON line, not a capture.
const tracingInfo = `{"timestamp":"2026-11-19T22:41:04.100000Z","level":"INFO","fields":{"message":"Inserted 2 rows","rows":2},"target":"rust_consumer::strategies::writer"}`

func TestTracing_InfoLine_GivesTheFieldMessageAndTheTargetAsScope(t *testing.T) {
	h := newHarness(t, defaultConfig())

	lr, scope := h.runScoped(input{body: tracingInfo})

	wantBody(t, lr, "Inserted 2 rows")
	wantSeverity(t, lr, plog.SeverityNumberInfo, "INFO")
	wantTime(t, lr, time.Date(2026, 11, 19, 22, 41, 4, 100000000, time.UTC))
	wantScope(t, scope, "rust_consumer::strategies::writer")
	a := lr.Attributes()
	attrInt(t, a, "vstan.tracing.rows", 2)
	if attrStr(t, a, "log.record.original") != tracingInfo {
		t.Fatalf("attributes = %v", a.AsRaw())
	}
	absent(t, a, "fields", "target", "message", "timestamp", "rows")
}

func TestTracing_TopLevelExtraKey_IsUnderTheBranchNamespace(t *testing.T) {
	h := newHarness(t, defaultConfig())

	lr, _ := h.runScoped(input{body: `{"timestamp":"2026-11-19T22:41:04Z","level":"INFO","fields":{"message":"m"},"target":"a::b","threadId":"ThreadId(2)"}`})

	if attrStr(t, lr.Attributes(), "vstan.tracing.threadId") != "ThreadId(2)" {
		t.Fatalf("attributes = %v", lr.Attributes().AsRaw())
	}
	absent(t, lr.Attributes(), "threadId")
}

func TestTracing_WarnLine_IsWarn(t *testing.T) {
	h := newHarness(t, defaultConfig())

	lr, _ := h.runScoped(input{body: `{"timestamp":"2026-11-19T22:41:04Z","level":"WARN","fields":{"message":"slow batch"},"target":"a::b"}`})

	wantSeverity(t, lr, plog.SeverityNumberWarn, "WARN")
}

func TestTracing_FieldsWithoutAMessage_IsUnchangedAndCounted(t *testing.T) {
	h := newHarness(t, defaultConfig())
	const bad = `{"timestamp":"2026-11-19T22:41:04Z","level":"INFO","fields":{"rows":2},"target":"a::b"}`

	lr, _ := h.runScoped(input{body: bad})

	wantUnchanged(t, lr, bad, map[string]any{})
	wantFailed(t, h, "tracing")
}
