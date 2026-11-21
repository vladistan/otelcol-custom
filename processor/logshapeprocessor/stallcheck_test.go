// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

// cspell:ignore vstan

package logshapeprocessor

import (
	"testing"
	"time"

	"go.opentelemetry.io/collector/pdata/plog"
)

// The line is the real sample of the ledger.
func TestStallCheck_PublishedLine_IsInfoWithTheValueAsAnAttribute(t *testing.T) {
	lr, _ := runW4(t, input{body: "published StallMinutes=2"})

	w4Body(t, lr, "published StallMinutes=2")
	w4Severity(t, lr, plog.SeverityNumberInfo, "INFO")
	attrInt(t, lr.Attributes(), "vstan.stallcheck.stall_minutes", 2)
}

func TestStallCheck_ContainerPrefix_IsDroppedAndGivesTheTime(t *testing.T) {
	lr, _ := runW4(t, input{body: "2026-11-19T22:56:53.123456789Z stdout F published StallMinutes=2"})

	w4Body(t, lr, "published StallMinutes=2")
	w4Time(t, lr, time.Date(2026, 11, 19, 22, 56, 53, 123456789, time.UTC))
	if attrStr(t, lr.Attributes(), "log.record.original") != "2026-11-19T22:56:53.123456789Z stdout F published StallMinutes=2" {
		t.Fatalf("attributes = %v", lr.Attributes().AsRaw())
	}
}

func TestStallCheck_OtherPublishedText_IsNotClaimed(t *testing.T) {
	const line = "published to the queue"

	lr, _ := runW4(t, input{body: line})

	w4Untouched(t, lr, line, map[string]any{})
}
