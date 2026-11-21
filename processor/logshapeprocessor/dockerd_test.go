// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"testing"
	"time"

	"go.opentelemetry.io/collector/pdata/plog"
)

// Docker daemon lines are logfmt, the format the logrus branch owns (one writer for it). These
// tests keep the daemon shape as a fixture and check what the whole table does with it.
// Invented fixture: the shape of a daemon line from the journal, not a capture.
const dockerdError = `time="2026-11-20T01:00:00.123456789+01:00" level=error msg="copy stream failed" error="reading from a closed fifo" stream=stdout`

func TestDockerd_ErrorLine_GivesTheMessageWithTheErrorTimeAndSeverity(t *testing.T) {
	lr, _ := runW4(t, input{body: dockerdError})

	w4Body(t, lr, "copy stream failed: reading from a closed fifo")
	w4Severity(t, lr, plog.SeverityNumberError, "ERROR")
	w4Time(t, lr, time.Date(2026, 11, 20, 0, 0, 0, 123456789, time.UTC))
}

func TestDockerd_WarningAndInfoLines_MapOnTheWordTable(t *testing.T) {
	lr, _ := runW4(t, input{body: `time="2026-11-20T01:00:00+01:00" level=warning msg="Your kernel does not support swap limit capabilities"`})
	w4Severity(t, lr, plog.SeverityNumberWarn, "WARN")

	lr, _ = runW4(t, input{body: `time="2026-11-20T01:00:00+01:00" level=info msg="Daemon has completed initialization"`})
	w4Severity(t, lr, plog.SeverityNumberInfo, "INFO")
	w4Body(t, lr, "Daemon has completed initialization")
}
