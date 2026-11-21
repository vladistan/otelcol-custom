// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

// cspell:ignore stderrhint exim

package logshapeprocessor

import (
	"testing"

	"go.opentelemetry.io/collector/pdata/plog"
)

// The body and the attributes are those of a real journal row of a mail container (the image
// path is changed): the stored severity is ERROR and no priority is kept, so the severity came from
// the stderr stream. The text names no level, so the stderrhint branch clears the severity.
const (
	eximBanner = " 4292 Exim version 4.98.2 uid=100 gid=101 pid=4292 D=80001"
	eximFull   = eximBanner + "\nHints DB:\nBerkeley DB: 5.3.28\nSupport for: iconv"
)

func eximRecord() input {
	return input{
		body:     eximBanner,
		attrs:    map[string]any{"log.source": "journal", "full_message": eximFull, "syslog.appname": "9f2c0d4e1a7b"},
		resource: map[string]any{"container.id": "9f2c0d4e1a7b", "container.image.name": "registry.example.org/mail/smtp", "service.name": "mail-smtp-1"},
	}
}

func TestStderrHint_ContainerRecordWithAnErrorFromTheStream_IsUnset(t *testing.T) {
	lr, _ := runWithSeverity(t, eximRecord(), plog.SeverityNumberError, "ERROR")

	w4Body(t, lr, eximBanner)
	w4Severity(t, lr, plog.SeverityNumberUnspecified, "")
	if attrStr(t, lr.Attributes(), "full_message") != eximFull {
		t.Fatalf("the whole message must stay in full_message: %v", lr.Attributes().AsRaw())
	}
}

func TestStderrHint_ServiceStaysTheContainerServiceNotTheSyslogIdentifier(t *testing.T) {
	h := newHarness(t, defaultConfig())
	ld := eximRecord().logs()
	lr := ld.ResourceLogs().At(0).ScopeLogs().At(0).LogRecords().At(0)
	lr.SetSeverityNumber(plog.SeverityNumberError)
	if err := h.proc.ConsumeLogs(t.Context(), ld); err != nil {
		t.Fatal(err)
	}

	res := h.sink.AllLogs()[0].ResourceLogs().At(0).Resource()

	if attrStr(t, res.Attributes(), "service.name") != "mail-smtp-1" {
		t.Fatalf("resource = %v", res.Attributes().AsRaw())
	}
}

func TestStderrHint_JournalRecordOfAHostService_KeepsItsSeverity(t *testing.T) {
	in := input{body: "something failed", attrs: map[string]any{"log.source": "journal"}, resource: map[string]any{"service.name": "sshd"}}

	lr, _ := runWithSeverity(t, in, plog.SeverityNumberError, "ERROR")

	w4Severity(t, lr, plog.SeverityNumberError, "ERROR")
}

func TestStderrHint_ContainerRecordWithAnotherSeverity_KeepsIt(t *testing.T) {
	lr, _ := runWithSeverity(t, eximRecord(), plog.SeverityNumberWarn, "WARN")

	w4Severity(t, lr, plog.SeverityNumberWarn, "WARN")
}

func TestStderrHint_ContainerRecordThatIsNotFromTheJournal_KeepsItsSeverity(t *testing.T) {
	in := eximRecord()
	in.attrs = map[string]any{"log.source": "docker"}

	lr, _ := runWithSeverity(t, in, plog.SeverityNumberError, "ERROR")

	w4Severity(t, lr, plog.SeverityNumberError, "ERROR")
}
