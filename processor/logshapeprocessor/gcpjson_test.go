// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"testing"
	"time"

	"go.opentelemetry.io/collector/pdata/plog"
)

// Invented fixture: the shape of a GCP structured line, not a capture.
const gcpInfo = `{"logging.googleapis.com/operation":"nri.(*plugin).StopPodSandbox.func1()","logging.googleapis.com/sourceLocation":"pkg/nri/nri.go","message":"nri: Done StopPodSandbox","severity":"info","timestamp":"2026-11-19T22:42:00.250Z"}`

func TestGcpJSON_InfoLine_GivesTheMessageAndTheCodeAttributes(t *testing.T) {
	h := newHarness(t, defaultConfig())

	lr, _ := h.runScoped(input{body: gcpInfo})

	wantBody(t, lr, "nri: Done StopPodSandbox")
	wantSeverity(t, lr, plog.SeverityNumberInfo, "INFO")
	wantTime(t, lr, time.Date(2026, 11, 19, 22, 42, 0, 250000000, time.UTC))
	a := lr.Attributes()
	if attrStr(t, a, "code.file.path") != "pkg/nri/nri.go" || attrStr(t, a, "code.function.name") != "nri.(*plugin).StopPodSandbox.func1" {
		t.Fatalf("attributes = %v", a.AsRaw())
	}
	if attrStr(t, a, "log.record.original") != gcpInfo {
		t.Fatalf("attributes = %v", a.AsRaw())
	}
	absent(t, a, "logging.googleapis.com/operation", "logging.googleapis.com/sourceLocation", "message", "severity", "timestamp")
}

func TestGcpJSON_SourceLocationObject_GivesFileLineAndFunction(t *testing.T) {
	h := newHarness(t, defaultConfig())

	lr, _ := h.runScoped(input{body: `{"message":"m","severity":"ERROR","timestamp":"2026-11-19T22:42:00Z","logging.googleapis.com/sourceLocation":{"file":"a/b.go","line":"42","function":"pkg.Run"}}`})

	wantSeverity(t, lr, plog.SeverityNumberError, "ERROR")
	a := lr.Attributes()
	if attrStr(t, a, "code.file.path") != "a/b.go" || attrStr(t, a, "code.function.name") != "pkg.Run" {
		t.Fatalf("attributes = %v", a.AsRaw())
	}
	attrInt(t, a, "code.line.number", 42)
}

func TestGcpJSON_ExtraKeys_AreUnderTheBranchNamespace(t *testing.T) {
	h := newHarness(t, defaultConfig())

	lr, _ := h.runScoped(input{body: `{"message":"m","severity":"info","timestamp":"2026-11-19T22:42:00Z","requestCount":5,"shard":"b"}`})

	attrInt(t, lr.Attributes(), "vstan.gcpjson.requestCount", 5)
	if attrStr(t, lr.Attributes(), "vstan.gcpjson.shard") != "b" {
		t.Fatalf("attributes = %v", lr.Attributes().AsRaw())
	}
	absent(t, lr.Attributes(), "requestCount", "shard")
}

func TestGcpJSON_UnknownSeverity_LeavesItUnset(t *testing.T) {
	h := newHarness(t, defaultConfig())

	lr, _ := h.runScoped(input{body: `{"message":"m","severity":"DEFAULT","timestamp":"2026-11-19T22:42:00Z"}`})

	wantBody(t, lr, "m")
	wantSeverity(t, lr, plog.SeverityNumberUnspecified, "")
}

func TestGcpJSON_BadTimestamp_IsUnchangedAndCounted(t *testing.T) {
	h := newHarness(t, defaultConfig())
	const bad = `{"message":"m","severity":"info","timestamp":"soon"}`

	lr, _ := h.runScoped(input{body: bad})

	wantUnchanged(t, lr, bad, map[string]any{})
	wantFailed(t, h, "gcpjson")
}
