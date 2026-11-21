// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"testing"
	"time"

	"go.opentelemetry.io/collector/pdata/plog"
)

// Invented fixture: the shape of a calico node line, not a capture.
const calicoInfo = `2026-11-19 22:53:59.699 [INFO][73] felix/summary.go 100: Summarising 4 dataplane reconciliation loops`

func TestCalico_InfoLine_GivesTheTextThreadAndSourceLocation(t *testing.T) {
	lr, _ := runW4(t, input{body: calicoInfo})

	w4Body(t, lr, "Summarising 4 dataplane reconciliation loops")
	w4Severity(t, lr, plog.SeverityNumberInfo, "INFO")
	w4Time(t, lr, time.Date(2026, 11, 19, 22, 53, 59, 699000000, time.UTC))
	a := lr.Attributes()
	attrInt(t, a, "thread.id", 73)
	attrInt(t, a, "code.line.number", 100)
	if attrStr(t, a, "code.file.path") != "felix/summary.go" || attrStr(t, a, "log.record.original") != calicoInfo {
		t.Fatalf("attributes = %v", a.AsRaw())
	}
}

func TestCalico_WarningAndError_MapOnTheWordTable(t *testing.T) {
	for level, num := range map[string]plog.SeverityNumber{"WARNING": plog.SeverityNumberWarn, "ERROR": plog.SeverityNumberError, "FATAL": plog.SeverityNumberFatal} {
		lr, _ := runW4(t, input{body: "2026-11-19 22:53:59.699 [" + level + "][1] felix/x.go 5: text"})
		if lr.SeverityNumber() != num {
			t.Fatalf("[%s] -> %d, want %d", level, lr.SeverityNumber(), num)
		}
	}
}

func TestCalico_ImpossibleDate_IsUnchangedAndCounted(t *testing.T) {
	h := newHarness(t, defaultConfig())
	const bad = "2026-13-45 99:99:99.000 [INFO][73] felix/summary.go 100: text"

	lr, _ := h.run(input{body: bad})

	w4Untouched(t, lr, bad, map[string]any{})
	w4Failed(t, h, "calico")
}
