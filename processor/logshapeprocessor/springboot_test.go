// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/collector/pdata/plog"
)

// Generic names only: the layouts are those of the Spring Boot console format, the texts are made up.
const (
	sbISO = "2026-11-22T14:38:13.666Z  INFO 1 --- [worker] Example                    : Started in 2.1 seconds"
	sbOld = "2026-11-22 14:38:13.666  WARN 4242 --- [main] c.e.demo.Service : Cache is cold"
	sbApp = "2026-11-22T14:38:13.666+01:00 ERROR 7 --- [demo-app] [nio-8080-exec-1] c.e.demo.Controller : Request failed"

	sbTrace = "\tat com.example.demo.Controller.handle(Controller.java:10)\n" +
		"Caused by: java.io.IOException: boom\n" +
		"\t... 1 more"
)

func sbRun(t *testing.T, body string) (plog.LogRecord, string) {
	t.Helper()
	return runScopedW4(t, input{body: body})
}

func sbRun2(t *testing.T, body string, attrs map[string]any) (plog.LogRecord, string) {
	t.Helper()
	return runScopedW4(t, input{body: body, attrs: attrs})
}

func TestSpringBoot_ISOFormWithZone_IsShaped(t *testing.T) {
	lr, scope := sbRun(t, sbISO)

	w4Body(t, lr, "[Example] Started in 2.1 seconds")
	w4Severity(t, lr, plog.SeverityNumberInfo, "INFO")
	w4Scope(t, scope, "Example")
	w4Time(t, lr, time.Date(2026, 11, 22, 14, 38, 13, 666000000, time.UTC))
	a := lr.Attributes()
	attrInt(t, a, "process.pid", 1)
	if attrStr(t, a, "thread.name") != "worker" || attrStr(t, a, "log.record.original") != sbISO {
		t.Fatalf("attributes = %v", a.AsRaw())
	}
	if _, ok := a.Get("vstan.springboot.application"); ok {
		t.Fatalf("no application bracket, yet %v", a.AsRaw())
	}
}

func TestSpringBoot_OlderFormWithNoZone_ReadsTheTimeAsUTC(t *testing.T) {
	lr, scope := sbRun(t, sbOld)

	w4Body(t, lr, "[c.e.demo.Service] Cache is cold")
	w4Severity(t, lr, plog.SeverityNumberWarn, "WARN")
	w4Scope(t, scope, "c.e.demo.Service")
	w4Time(t, lr, time.Date(2026, 11, 22, 14, 38, 13, 666000000, time.UTC))
	attrInt(t, lr.Attributes(), "process.pid", 4242)
	if got := attrStr(t, lr.Attributes(), "thread.name"); got != "main" {
		t.Fatalf("thread.name = %q", got)
	}
}

func TestSpringBoot_ApplicationBracketForm_KeepsTheApplicationAndTheOffset(t *testing.T) {
	lr, scope := sbRun(t, sbApp)

	w4Body(t, lr, "[c.e.demo.Controller] Request failed")
	w4Severity(t, lr, plog.SeverityNumberError, "ERROR")
	w4Scope(t, scope, "c.e.demo.Controller")
	w4Time(t, lr, time.Date(2026, 11, 22, 13, 38, 13, 666000000, time.UTC))
	a := lr.Attributes()
	if attrStr(t, a, "thread.name") != "nio-8080-exec-1" || attrStr(t, a, "vstan.springboot.application") != "demo-app" {
		t.Fatalf("attributes = %v", a.AsRaw())
	}
}

func TestSpringBoot_EveryLevelToken_GivesItsSeverity(t *testing.T) {
	for _, c := range []struct {
		token string
		num   plog.SeverityNumber
	}{
		{"TRACE", plog.SeverityNumberTrace}, {"DEBUG", plog.SeverityNumberDebug}, {"INFO", plog.SeverityNumberInfo},
		{"WARN", plog.SeverityNumberWarn}, {"ERROR", plog.SeverityNumberError}, {"FATAL", plog.SeverityNumberFatal},
	} {
		lr, _ := sbRun(t, "2026-11-22T14:38:13.666Z "+c.token+" 1 --- [main] Example : text")
		w4Severity(t, lr, c.num, c.token)
	}
}

func TestSpringBoot_LevelTokenOutsideTheSix_IsNotClaimedAndNotCounted(t *testing.T) {
	const line = "2026-11-22T14:38:13.666Z NOTICE 1 --- [main] Example : text"
	h := newHarness(t, defaultConfig())

	lr, _ := h.run(input{body: line})

	w4Untouched(t, lr, line, map[string]any{})
	if got := h.counter(parseFailedMetric, "format"); len(got) != 0 {
		t.Fatalf("parse_failed = %v, want none", got)
	}
}

func TestSpringBoot_NonNumericPid_IsNotClaimed(t *testing.T) {
	const line = "2026-11-22T14:38:13.666Z  INFO abc --- [main] Example : text"

	lr, _ := sbRun(t, line)

	w4Untouched(t, lr, line, map[string]any{})
}

func TestSpringBoot_PaddedLogger_LosesThePadding(t *testing.T) {
	lr, scope := sbRun(t, "2026-11-22T14:38:13.666Z  INFO 1 --- [main] c.e.demo.Short                           : padded")

	w4Body(t, lr, "[c.e.demo.Short] padded")
	w4Scope(t, scope, "c.e.demo.Short")
}

func TestSpringBoot_LoggerLongerThanThePad_StaysWhole(t *testing.T) {
	const logger = "com.example.demo.deeply.nested.package.ServiceImplementation"

	lr, scope := sbRun(t, "2026-11-22T14:38:13.666Z  INFO 1 --- [main] "+logger+" : long")

	w4Body(t, lr, "["+logger+"] long")
	w4Scope(t, scope, logger)
}

func TestSpringBoot_EmptyMessage_GivesTheLoggerOnly(t *testing.T) {
	for _, line := range []string{
		"2026-11-22T14:38:13.666Z  INFO 1 --- [main] Example                    :",
		"2026-11-22T14:38:13.666Z  INFO 1 --- [main] Example                    : ",
	} {
		lr, _ := sbRun(t, line)

		w4Body(t, lr, "[Example]")
	}
}

func TestSpringBoot_PaddedThreadName_IsTrimmed(t *testing.T) {
	lr, _ := sbRun(t, "2026-11-22T14:38:13.666Z  INFO 1 --- [   scheduling-1] Example : tick")

	if got := attrStr(t, lr.Attributes(), "thread.name"); got != "scheduling-1" {
		t.Fatalf("thread.name = %q", got)
	}
}

func TestSpringBoot_MergedStackTrace_IsClaimedByItsFirstLine(t *testing.T) {
	merged := sbApp + "\n" + sbTrace

	lr, _ := sbRun(t, merged)

	w4Body(t, lr, "[c.e.demo.Controller] Request failed")
	w4Severity(t, lr, plog.SeverityNumberError, "ERROR")
	if got := attrStr(t, lr.Attributes(), "log.record.original"); got != merged {
		t.Fatalf("log.record.original = %q, want the whole merged text", got)
	}
}

func TestSpringBoot_MergedStackTraceWithNoStacktraceAttribute_PutsTheWholeTextInIt(t *testing.T) {
	merged := sbApp + "\n" + sbTrace

	lr, _ := sbRun(t, merged)

	w4Body(t, lr, "[c.e.demo.Controller] Request failed")
	if got := attrStr(t, lr.Attributes(), "exception.stacktrace"); got != merged {
		t.Fatalf("exception.stacktrace = %q, want %q", got, merged)
	}
}

func TestSpringBoot_MergedStackTraceWithExceptionAttributes_KeepsThemAndCutsTheBody(t *testing.T) {
	const stack = "whole trace set by logrecombine"
	attrs := map[string]any{
		"exception.type":       "java.io.IOException",
		"exception.message":    "boom",
		"exception.stacktrace": stack,
	}

	lr, _ := sbRun2(t, sbApp+"\n"+sbTrace, attrs)

	w4Body(t, lr, "[c.e.demo.Controller] Request failed")
	for key, want := range attrs {
		if got := attrStr(t, lr.Attributes(), key); got != want {
			t.Fatalf("%s = %q, want %q", key, got, want)
		}
	}
}

func TestSpringBoot_SingleLineRecord_GivesNoStacktraceAttribute(t *testing.T) {
	lr, _ := sbRun(t, sbApp)

	if v, ok := lr.Attributes().Get("exception.stacktrace"); ok {
		t.Fatalf("exception.stacktrace = %v, want it absent", v.AsRaw())
	}
}

func TestSpringBoot_ContinuationLinesAsSeparateRecords_PassUnchanged(t *testing.T) {
	for _, line := range strings.Split(sbTrace, "\n") {
		lr, _ := sbRun(t, line)

		w4Untouched(t, lr, line, map[string]any{})
	}
}

func TestSpringBoot_GoCDWrapperLine_IsClaimedByGoCDOnly(t *testing.T) {
	lr, _ := runW4(t, input{body: gocdInfo})

	a := lr.Attributes()
	if attrStr(t, a, "code.function.name") != "ConfigRepository" {
		t.Fatalf("gocd did not claim it: %v", a.AsRaw())
	}
	if _, ok := a.Get("process.pid"); ok {
		t.Fatalf("springboot claimed a GoCD line: %v", a.AsRaw())
	}
}

func TestSpringBoot_SpringBootLine_IsNotClaimedByGoCD(t *testing.T) {
	for _, line := range []string{sbISO, sbOld, sbApp} {
		lr, _ := sbRun(t, line)

		if _, ok := lr.Attributes().Get("code.function.name"); ok {
			t.Fatalf("gocd claimed %q: %v", line, lr.Attributes().AsRaw())
		}
	}
}

func TestSpringBoot_NoBranchBeforeItTakesASpringBootLine(t *testing.T) {
	for _, line := range []string{sbISO, sbOld, sbApp} {
		lr, _ := sbRun(t, line)

		if _, ok := lr.Attributes().Get("process.pid"); !ok {
			t.Fatalf("another branch claimed %q: %v", line, lr.Attributes().AsRaw())
		}
	}
}

func TestSpringBoot_Gate_AdmitsTheThreeFormsAndAMergedTrace(t *testing.T) {
	g := gateFor("springboot", defaultConfig())
	var s seen

	for _, line := range []string{sbISO, sbOld, sbApp, sbApp + "\n" + sbTrace} {
		if !g.admits(&rec{body: line}, &s) {
			t.Errorf("gate refuses %q", line)
		}
	}
}

func TestSpringBoot_Gate_RefusesBodiesWithoutTheMarker(t *testing.T) {
	g := gateFor("springboot", defaultConfig())
	var s seen

	for _, line := range []string{
		"2026-11-22T14:38:13.666Z  INFO nothing to see",
		gocdInfo,
		"plain text",
		"",
	} {
		if g.admits(&rec{body: line}, &s) {
			t.Errorf("gate admits %q", line)
		}
	}
}
