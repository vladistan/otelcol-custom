// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"testing"

	"go.opentelemetry.io/collector/pdata/plog"
)

func serialInput(body string) input {
	return input{
		body: body,
		attrs: map[string]any{
			"cloud.resource_id": "serialconsole.googleapis.com/serial_port_1_output",
			"is_text_escaped":   true,
			"gcp.label.compute.googleapis.com/resource_name": "gk3-cluster-a-nap-w7v5",
		},
	}
}

// The bodies are the escaped text of the console: the backslashes are in the string.
const (
	serialOK     = `[\x1b[0;32m  OK  \x1b[0m] Started Daily apt download activities.\r\n`
	serialFailed = `[\x1b[0;1;31mFAILED\x1b[0m] Failed to start Docker Application Container Engine.\r\n`
	serialJob    = `[\x1b[0;31m*     \x1b[0m] A start job is running for Wait for Network (11s / 1min 30s)\r`
)

func TestSerialConsole_OKLine_IsUnescapedStrippedAndInfo(t *testing.T) {
	lr, res := runW4(t, serialInput(serialOK))

	w4Body(t, lr, "[  OK  ] Started Daily apt download activities.")
	w4Severity(t, lr, plog.SeverityNumberInfo, "INFO")
	if attrStr(t, res.Attributes(), "host.name") != "gk3-cluster-a-nap-w7v5" || attrStr(t, lr.Attributes(), "log.record.original") != serialOK {
		t.Fatalf("resource = %v attributes = %v", res.Attributes().AsRaw(), lr.Attributes().AsRaw())
	}
}

func TestSerialConsole_FailedLine_IsError(t *testing.T) {
	lr, _ := runW4(t, serialInput(serialFailed))

	w4Body(t, lr, "[FAILED] Failed to start Docker Application Container Engine.")
	w4Severity(t, lr, plog.SeverityNumberError, "ERROR")
}

func TestSerialConsole_RunningJobLine_IsWarn(t *testing.T) {
	lr, _ := runW4(t, serialInput(serialJob))

	w4Body(t, lr, "[*     ] A start job is running for Wait for Network (11s / 1min 30s)")
	w4Severity(t, lr, plog.SeverityNumberWarn, "WARN")
}

func TestSerialConsole_KernelLine_LeavesTheSeverityUnset(t *testing.T) {
	lr, _ := runW4(t, serialInput(`[    1.234567] Linux version 6.1.0\r\n`))

	w4Body(t, lr, "[    1.234567] Linux version 6.1.0")
	w4Severity(t, lr, plog.SeverityNumberUnspecified, "")
}

func TestSerialConsole_EscapedBytes_FormUtf8(t *testing.T) {
	lr, _ := runW4(t, serialInput(`Starting \xe2\x80\xa6\r\n`))

	w4Body(t, lr, "Starting …")
}

func TestSerialConsole_BareCRLFRecord_BecomesAnEmptyBody(t *testing.T) {
	lr, _ := runW4(t, serialInput(`\r\n`))

	w4Body(t, lr, "")
}

func TestSerialConsole_OtherSource_IsNotClaimed(t *testing.T) {
	in := input{body: serialOK, attrs: map[string]any{"cloud.resource_id": "other"}}

	lr, _ := runW4(t, in)

	if lr.Body().Str() != serialOK || lr.SeverityNumber() != plog.SeverityNumberUnspecified {
		t.Fatalf("record changed: %q", lr.Body().Str())
	}
}
