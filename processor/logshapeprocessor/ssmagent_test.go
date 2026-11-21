// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"testing"

	"go.opentelemetry.io/collector/pdata/plog"
)

// Invented fixture: the shape of an amazon-ssm-agent line, not a capture.
const ssmWarn = `2026-11-19 23:00:14.0871 WARN EC2RoleProvider Failed to connect to Systems Manager`

func TestSSMAgent_WarnLine_GivesTheMessageScopeAndSeverity(t *testing.T) {
	lr, scope := runScopedW4(t, input{body: ssmWarn})

	w4Body(t, lr, "Failed to connect to Systems Manager")
	w4Severity(t, lr, plog.SeverityNumberWarn, "WARN")
	w4Scope(t, scope, "EC2RoleProvider")
	if attrStr(t, lr.Attributes(), "log.record.original") != ssmWarn {
		t.Fatalf("attributes = %v", lr.Attributes().AsRaw())
	}
}

func TestSSMAgent_Levels_MapOnTheWordTable(t *testing.T) {
	for level, num := range map[string]plog.SeverityNumber{"INFO": plog.SeverityNumberInfo, "ERROR": plog.SeverityNumberError, "DEBUG": plog.SeverityNumberDebug} {
		lr, _ := runW4(t, input{body: "2026-11-19 23:00:14.0871 " + level + " Updater text"})
		if lr.SeverityNumber() != num {
			t.Fatalf("%s -> %d, want %d", level, lr.SeverityNumber(), num)
		}
	}
}

func TestSSMAgent_LineWithAnotherLevelWord_IsNotClaimed(t *testing.T) {
	const line = "2026-11-19 23:00:14.0871 CHATTY Updater text"

	lr, _ := runW4(t, input{body: line})

	w4Untouched(t, lr, line, map[string]any{})
}
