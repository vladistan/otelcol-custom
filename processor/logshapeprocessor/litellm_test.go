// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"context"
	"testing"

	"go.opentelemetry.io/collector/pdata/plog"
)

// The body and the record attributes are those of a real journal row of a litellm container:
// the stored severity is ERROR, which the stream gave, and the text says INFO. The container
// name and the invocation attributes are left out. The branch is litellm: a level inside the text
// beats a severity that came with the record.
const (
	litellmGreen = "\x1b[92m"
	litellmReset = "\x1b[0m"
	litellmInfo  = litellmGreen + "07:13:58 - LiteLLM Proxy:INFO" + litellmReset +
		": policy_registry.py:577 - Synced 0 production policies and 0 draft/published (by ID) from DB to in-memory registry"
	litellmFull = "07:13:58 - LiteLLM Proxy:INFO: policy_registry.py:577 - Synced 0 production policies\n" +
		"07:13:58 - LiteLLM Proxy:INFO: attachment_registry.py:479 - Synced attachments"
)

// journalContainer is a journal record of a container: the source is journal, the container is in the
// resource and the whole message, all its lines, is in full_message.
func journalContainer(body string) input {
	return input{
		body:     body,
		attrs:    map[string]any{"log.source": "journal", "full_message": litellmFull, "syslog.appname": "litellm"},
		resource: map[string]any{"container.name": "litellm", "service.name": "litellm", "host.name": "docker-host"},
	}
}

// runWithSeverity sends one record that already carries a severity, as the journald receiver
// gives a stderr line, and returns it with its scope name.
func runWithSeverity(t *testing.T, in input, num plog.SeverityNumber, text string) (plog.LogRecord, string) {
	t.Helper()
	h := newHarness(t, defaultConfig())
	ld := in.logs()
	lr := ld.ResourceLogs().At(0).ScopeLogs().At(0).LogRecords().At(0)
	lr.SetSeverityNumber(num)
	lr.SetSeverityText(text)
	if err := h.proc.ConsumeLogs(context.Background(), ld); err != nil {
		t.Fatal(err)
	}
	out := h.sink.AllLogs()[0].ResourceLogs().At(0).ScopeLogs().At(0)
	return out.LogRecords().At(0), out.Scope().Name()
}

func TestLitellm_EmbeddedInfoWithAnErrorFromTheStream_GivesInfo(t *testing.T) {
	lr, scope := runWithSeverity(t, journalContainer(litellmInfo), plog.SeverityNumberError, "ERROR")

	w4Body(t, lr, "Synced 0 production policies and 0 draft/published (by ID) from DB to in-memory registry")
	w4Severity(t, lr, plog.SeverityNumberInfo, "INFO")
	w4Scope(t, scope, "LiteLLM Proxy")
	a := lr.Attributes()
	attrInt(t, a, "code.line.number", 577)
	if attrStr(t, a, "code.file.path") != "policy_registry.py" || attrStr(t, a, "log.record.original") != litellmInfo {
		t.Fatalf("attributes = %v", a.AsRaw())
	}
	if attrStr(t, a, "full_message") != litellmFull {
		t.Fatalf("the whole message must stay in full_message: %v", a.AsRaw())
	}
}

func TestLitellm_EmbeddedWarning_BeatsAnInfoSeverity(t *testing.T) {
	lr, _ := runWithSeverity(t, input{body: "23:01:22 - LiteLLM:WARNING: utils.py:77 - Slow upstream"}, plog.SeverityNumberInfo, "INFO")

	w4Severity(t, lr, plog.SeverityNumberWarn, "WARN")
}

func TestLitellm_UnknownEmbeddedLevel_LeavesTheSeverityUnset(t *testing.T) {
	lr, _ := runWithSeverity(t, input{body: "23:01:22 - LiteLLM:CHATTY: utils.py:77 - text"}, plog.SeverityNumberError, "ERROR")

	w4Body(t, lr, "text")
	w4Severity(t, lr, plog.SeverityNumberUnspecified, "")
}

func TestLitellm_PlainTextWithAColon_IsNotClaimed(t *testing.T) {
	const line = "23:01:22 - done: all good and the work is finished" // 30 B or more: a shorter one hits a bounds panic in the sentry claims

	lr, _ := runW4(t, input{body: line})

	w4Untouched(t, lr, line, map[string]any{})
}
