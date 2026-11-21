// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"testing"

	"go.opentelemetry.io/collector/pdata/plog"
)

// INVENTED fixtures: the "Device:" layout follows smartd output, the counts and attribute lines are made up.
func TestSmartd_UncorrectableSectors_AreWarn(t *testing.T) {
	for _, line := range []string{
		"Device: /dev/ada3, 8 Offline uncorrectable sectors",
		"Device: /dev/sda [SAT], 2 Currently unreadable (pending) sectors",
	} {
		lr, _ := runW4(t, input{body: line})

		w4Severity(t, lr, plog.SeverityNumberWarn, "WARN")
		w4Body(t, lr, line)
		if attrStr(t, lr.Attributes(), "log.record.original") != line {
			t.Fatalf("attributes = %v", lr.Attributes().AsRaw())
		}
	}
}

func TestSmartd_FailedHealth_IsError(t *testing.T) {
	lr, _ := runW4(t, input{body: "Device: /dev/ada3, FAILED SMART self-check. BACKUP DATA NOW!"})

	w4Severity(t, lr, plog.SeverityNumberError, "ERROR")
}

func TestSmartd_RoutineLine_LeavesTheSeverityUnset(t *testing.T) {
	lr, _ := runW4(t, input{body: "Device: /dev/ada3, SMART Prefailure Attribute: 1 Raw_Read_Error_Rate changed from 100 to 99"})

	w4Severity(t, lr, plog.SeverityNumberUnspecified, "")
}

func TestSmartd_DeviceTextThatIsNotSmartd_IsNotClaimed(t *testing.T) {
	const line = "Device: not a path"

	lr, _ := runW4(t, input{body: line})

	w4Untouched(t, lr, line, map[string]any{})
}
