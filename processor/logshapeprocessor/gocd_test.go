// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"testing"

	"go.opentelemetry.io/collector/pdata/plog"
)

// INVENTED fixtures: the "jvm 1 |" wrapper and the line layout follow a GoCD server log, the
// texts are made up. Replace them with real lines when they are delivered.
const (
	gocdInfo = "jvm 1    | 2026-11-19 22:00:01,123 INFO  [qtp1-5] ConfigRepository:123 - Loaded 3 pipelines"
	gocdWarn = "jvm 1    | 2026-11-19 22:00:02,000 WARN  [qtp1-6] MaterialUpdate:7 - Slow poll"
	gocdAt   = "jvm 1    |     at com.thoughtworks.go.Server.run(Server.java:42)"
)

func TestGoCD_SingleLine_LosesThePrefixTimeAndLevel(t *testing.T) {
	lr, _ := runW4(t, input{body: gocdInfo})

	w4Body(t, lr, "Loaded 3 pipelines")
	w4Severity(t, lr, plog.SeverityNumberInfo, "INFO")
	a := lr.Attributes()
	if attrStr(t, a, "thread.name") != "qtp1-5" || attrStr(t, a, "code.function.name") != "ConfigRepository" || attrStr(t, a, "log.record.original") != gocdInfo {
		t.Fatalf("attributes = %v", a.AsRaw())
	}
	attrInt(t, a, "code.line.number", 123)
	if lr.Timestamp() != 0 {
		t.Fatalf("a time without a zone must not set the record time: %v", lr.Timestamp())
	}
}

func TestGoCD_JournaldBatch_StaysOneRecordWithTheHighestSeverity(t *testing.T) {
	batch := gocdInfo + "\n" + gocdWarn + "\n" + gocdAt
	lr, _ := runW4(t, input{body: batch})

	w4Body(t, lr, "Loaded 3 pipelines\nSlow poll\n    at com.thoughtworks.go.Server.run(Server.java:42)")
	w4Severity(t, lr, plog.SeverityNumberWarn, "WARN")
	if attrStr(t, lr.Attributes(), "thread.name") != "qtp1-5" {
		t.Fatalf("attributes come from the first line: %v", lr.Attributes().AsRaw())
	}
}

func TestGoCD_TextWithoutTheWrapperPrefix_IsNotClaimed(t *testing.T) {
	const line = "2026-11-19 22:00:01,123 INFO  [qtp1-5] ConfigRepository:123 - Loaded 3 pipelines"

	lr, _ := runW4(t, input{body: line})

	w4Untouched(t, lr, line, map[string]any{})
}

func TestGoCD_PrefixedLineThatIsNotALogLine_IsUnchangedAndCounted(t *testing.T) {
	h := newHarness(t, defaultConfig())
	const bad = "jvm 1    | just some output"

	lr, _ := h.run(input{body: bad})

	w4Untouched(t, lr, bad, map[string]any{})
	w4Failed(t, h, "gocd")
}
