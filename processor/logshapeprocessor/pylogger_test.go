// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"strings"
	"testing"

	"go.opentelemetry.io/collector/pdata/plog"
)

// The layout is the Python logging default (%(levelname)s:%(name)s:%(message)s). The fixtures are made up,
// except pyRealLine: a line of a production service, with the service and host names left out.
const (
	pyRealLine = "ERROR:opentelemetry.sdk.metrics._internal.export:Exception while exporting metrics"
	pyHeader   = "ERROR:app.core:Request failed"
	pyTrace    = "Traceback (most recent call last):\n" +
		"  File \"app.py\", line 1, in handle\n" +
		"ValueError: boom"
)

func pyRun(t *testing.T, body string) (plog.LogRecord, string) {
	t.Helper()
	return runScopedW4(t, input{body: body})
}

func TestPyLogger_EveryLevelToken_IsClaimedWithItsSeverity(t *testing.T) {
	for _, c := range []struct {
		token string
		num   plog.SeverityNumber
		text  string
	}{
		{"DEBUG", plog.SeverityNumberDebug, "DEBUG"}, {"INFO", plog.SeverityNumberInfo, "INFO"},
		{"WARNING", plog.SeverityNumberWarn, "WARN"}, {"WARN", plog.SeverityNumberWarn, "WARN"},
		{"ERROR", plog.SeverityNumberError, "ERROR"}, {"CRITICAL", plog.SeverityNumberFatal, "FATAL"},
		{"FATAL", plog.SeverityNumberFatal, "FATAL"},
	} {
		t.Run(c.token, func(t *testing.T) {
			lr, scope := pyRun(t, c.token+":app:text")

			w4Severity(t, lr, c.num, c.text)
			w4Scope(t, scope, "app")
			w4Body(t, lr, "[app] text")
		})
	}
}

func TestPyLogger_RealLoggerLineWithUnderscoreAndDots_IsClaimed(t *testing.T) {
	const logger = "opentelemetry.sdk.metrics._internal.export"

	lr, scope := pyRun(t, pyRealLine)

	w4Scope(t, scope, logger)
	w4Body(t, lr, "["+logger+"] Exception while exporting metrics")
	w4Severity(t, lr, plog.SeverityNumberError, "ERROR")
	if got := attrStr(t, lr.Attributes(), "log.record.original"); got != pyRealLine {
		t.Fatalf("log.record.original = %q", got)
	}
}

func TestPyLogger_TokenThatIsNotALevel_IsNotClaimedAndNotCounted(t *testing.T) {
	const line = "LOG:app:text"
	h := newHarness(t, defaultConfig())

	lr, _ := h.run(input{body: line})

	w4Untouched(t, lr, line, map[string]any{})
	if got := h.counter(parseFailedMetric, "format"); len(got) != 0 {
		t.Fatalf("parse_failed = %v, want none", got)
	}
}

func TestPyLogger_DottedLoggerAndColonInTheMessage_GiveScopeBodyAndOriginal(t *testing.T) {
	const line = "INFO:app.core:key: value"

	lr, scope := pyRun(t, line)

	w4Scope(t, scope, "app.core")
	w4Body(t, lr, "[app.core] key: value")
	if got := attrStr(t, lr.Attributes(), "log.record.original"); got != line {
		t.Fatalf("log.record.original = %q", got)
	}
}

func TestPyLogger_LoggerEndsAtTheSecondColon(t *testing.T) {
	lr, scope := pyRun(t, "ERROR:app:db:connection failed")

	w4Scope(t, scope, "app")
	w4Body(t, lr, "[app] db:connection failed")
}

func TestPyLogger_EmptyMessage_GivesTheLoggerOnly(t *testing.T) {
	lr, _ := pyRun(t, "INFO:app:")

	w4Body(t, lr, "[app]")
}

func TestPyLogger_LoggerOutsideLettersDigitsUnderscoreAndDot_IsNotClaimed(t *testing.T) {
	for _, line := range []string{"INFO:my-app:text", "INFO:my app:text", "INFO::text"} {
		t.Run(line, func(t *testing.T) {
			lr, _ := pyRun(t, line)

			w4Untouched(t, lr, line, map[string]any{})
		})
	}
}

func TestPyLogger_UvicornLine_IsClaimedByUvicornOnly(t *testing.T) {
	lr, scope := pyRun(t, "INFO:     started")

	w4Scope(t, scope, "")
	w4Body(t, lr, "started")
	w4Severity(t, lr, plog.SeverityNumberInfo, "INFO")
}

func TestPyLogger_TrailingCarriageReturn_IsTrimmedBeforeTheClaim(t *testing.T) {
	lr, scope := pyRun(t, "INFO:app:text\r")

	w4Scope(t, scope, "app")
	w4Body(t, lr, "[app] text")
}

func TestPyLogger_MergedTraceback_IsClaimedByItsFirstLineAndKeepsTheLevelOfIt(t *testing.T) {
	merged := "WARNING:app:Request failed\n" + pyTrace

	lr, _ := pyRun(t, merged)

	w4Body(t, lr, "[app] Request failed")
	w4Severity(t, lr, plog.SeverityNumberWarn, "WARN")
	if got := attrStr(t, lr.Attributes(), "log.record.original"); got != merged {
		t.Fatalf("log.record.original = %q, want the whole merged text", got)
	}
}

func TestPyLogger_TracebackLinesAsSeparateRecords_PassUnchanged(t *testing.T) {
	for _, line := range strings.Split(pyTrace, "\n") {
		t.Run(line, func(t *testing.T) {
			lr, _ := pyRun(t, line)

			w4Untouched(t, lr, line, map[string]any{})
		})
	}
}

func TestPyLogger_NoBranchBeforeItTakesAPythonLoggingLine(t *testing.T) {
	for line, want := range map[string]string{
		pyHeader:            "[app.core] Request failed",
		"DEBUG:root:x":      "[root] x",
		"CRITICAL:a_b.c1:x": "[a_b.c1] x",
	} {
		t.Run(line, func(t *testing.T) {
			lr, _ := pyRun(t, line)

			w4Body(t, lr, want)
		})
	}
}

func TestPyLogger_Gate_AdmitsTheLevelTokensAndAMergedTraceback(t *testing.T) {
	g := gateFor("pylogger", defaultConfig())
	var s seen

	for _, line := range []string{"INFO:a:b", "WARN:a:b", "WARNING:a:b", "CRITICAL:a:b", "FATAL:a:b", "DEBUG:a:b", "ERROR:a:b", pyHeader + "\n" + pyTrace} {
		if !g.admits(&rec{body: line}, &s) {
			t.Errorf("gate refuses %q", line)
		}
	}
}

func TestPyLogger_Gate_RefusesBodiesThatCannotBeALoggingLine(t *testing.T) {
	g := gateFor("pylogger", defaultConfig())
	var s seen

	for _, line := range []string{"", "plain text", "TRACE:a:b", "2026-11-22T14:38:13Z INFO a:b", "INFO"} {
		if g.admits(&rec{body: line}, &s) {
			t.Errorf("gate admits %q", line)
		}
	}
}
