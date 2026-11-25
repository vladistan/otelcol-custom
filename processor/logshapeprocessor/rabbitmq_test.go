// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"testing"
	"time"

	"go.opentelemetry.io/collector/pdata/plog"
)

// INVENTED values in the shape of a real rabbitmq line: ISO time with an offset, [level], an Erlang
// pid and a trailing colour reset. Replace it with a real line when one is delivered.
const rabbitInfo = "2026-11-20 00:27:56.299641+00:00 [info] <0.18182677.0> User 'monitor' authenticated successfully\x1b[0m"

func TestRabbitMQ_InfoLine_GivesTheMessageWithoutTheAnsiAndThePid(t *testing.T) {
	lr, _ := runW4(t, input{body: rabbitInfo})

	w4Body(t, lr, "User 'monitor' authenticated successfully")
	w4Severity(t, lr, plog.SeverityNumberInfo, "INFO")
	w4Time(t, lr, time.Date(2026, 11, 20, 0, 27, 56, 299641000, time.UTC))
	a := lr.Attributes()
	if attrStr(t, a, "vstan.rabbitmq.pid") != "<0.18182677.0>" || attrStr(t, a, "log.record.original") != rabbitInfo {
		t.Fatalf("attributes = %v", a.AsRaw())
	}
}

func TestRabbitMQ_LevelWords_MapOnTheWordTable(t *testing.T) {
	want := map[string]plog.SeverityNumber{
		"debug": plog.SeverityNumberDebug, "warning": plog.SeverityNumberWarn, "error": plog.SeverityNumberError,
		"critical": plog.SeverityNumberFatal, "notice": plog.SeverityNumberInfo,
	}
	for level, num := range want {
		lr, _ := runW4(t, input{body: "2026-11-20 00:27:56.299641+00:00 [" + level + "] <0.1.0> text"})
		if lr.SeverityNumber() != num {
			t.Fatalf("[%s] -> %d, want %d", level, lr.SeverityNumber(), num)
		}
	}
}

func TestRabbitMQ_LineWithoutAPid_GivesTheMessage(t *testing.T) {
	lr, _ := runW4(t, input{body: "2026-11-20 00:27:56.299641+00:00 [info] Server startup complete"})

	w4Body(t, lr, "Server startup complete")
}

func TestRabbitMQ_UnknownLevelWord_LeavesTheSeverityUnset(t *testing.T) {
	lr, _ := runW4(t, input{body: "2026-11-20 00:27:56.299641+00:00 [chatty] <0.1.0> text"})

	w4Body(t, lr, "text")
	w4Severity(t, lr, plog.SeverityNumberUnspecified, "")
}

func TestRabbitMQ_ImpossibleDate_IsUnchangedAndCounted(t *testing.T) {
	h := newHarness(t, defaultConfig())
	const bad = "2026-13-45 99:99:99.000000+00:00 [info] <0.1.0> text"

	lr, _ := h.run(input{body: bad})

	w4Untouched(t, lr, bad, map[string]any{})
	w4Failed(t, h, "rabbitmq")
}
