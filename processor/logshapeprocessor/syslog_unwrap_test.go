// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

// cspell:ignore vstan

package logshapeprocessor

import (
	"testing"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
)

// The wrapper shapes follow real forwarded lines; the host name and the inner texts are invented.
const (
	wrapSmartd5424 = "1 2026-11-20T00:03:18.148343+01:00 nas smartd 1517 - - Device: /dev/sda [SAT], 2 Currently unreadable (pending) sectors"
	wrapSSMIso     = "2026-11-20T00:03:18.148343+01:00 docker-host amazon-ssm-agent[1517]: " + ssmWarn
	wrapSSM3164    = "Nov 20 00:03:18 docker-host amazon-ssm-agent[1517]: " + ssmWarn
	wrapPlainIso   = "2026-11-20T00:03:18.148343+01:00 nas sshd[12]: Accepted publickey for tyler"
)

func resText(t *testing.T, res pcommon.Resource, key string) string {
	t.Helper()
	return attrStr(t, res.Attributes(), key)
}

func TestSyslogUnwrap_RawRFC5424Smartd_GivesTheInnerSeverityHostServiceAndTime(t *testing.T) {
	lr, res := runW4(t, input{body: wrapSmartd5424})

	w4Body(t, lr, "Device: /dev/sda [SAT], 2 Currently unreadable (pending) sectors")
	w4Severity(t, lr, plog.SeverityNumberWarn, "WARN")
	w4Time(t, lr, time.Date(2026, 11, 19, 23, 3, 18, 148343000, time.UTC))
	if resText(t, res, "host.name") != "nas" || resText(t, res, "service.name") != "smartd" {
		t.Fatalf("resource = %v", res.Attributes().AsRaw())
	}
	attrInt(t, lr.Attributes(), "process.pid", 1517)
	if attrStr(t, lr.Attributes(), "vstan.smartd.device") != "/dev/sda" {
		t.Fatalf("attributes = %v", lr.Attributes().AsRaw())
	}
}

func TestSyslogUnwrap_IsoWrapperAroundAnAgentLine_GivesTheInnerParse(t *testing.T) {
	lr, res := runW4(t, input{body: wrapSSMIso})

	w4Body(t, lr, "Failed to connect to Systems Manager")
	w4Severity(t, lr, plog.SeverityNumberWarn, "WARN")
	if resText(t, res, "host.name") != "docker-host" || resText(t, res, "service.name") != "amazon-ssm-agent" {
		t.Fatalf("resource = %v", res.Attributes().AsRaw())
	}
	attrInt(t, lr.Attributes(), "process.pid", 1517)
}

func TestSyslogUnwrap_Rfc3164WrapperAroundAnAgentLine_GivesTheInnerParse(t *testing.T) {
	lr, res := runW4(t, input{body: wrapSSM3164})

	w4Body(t, lr, "Failed to connect to Systems Manager")
	w4Severity(t, lr, plog.SeverityNumberWarn, "WARN")
	if resText(t, res, "host.name") != "docker-host" || resText(t, res, "service.name") != "amazon-ssm-agent" {
		t.Fatalf("resource = %v", res.Attributes().AsRaw())
	}
}

func TestSyslogUnwrap_IsoWrapperAroundPlainText_GivesHostServicePidAndTime(t *testing.T) {
	lr, res := runW4(t, input{body: wrapPlainIso})

	w4Body(t, lr, "Accepted publickey for tyler")
	w4Severity(t, lr, plog.SeverityNumberUnspecified, "")
	w4Time(t, lr, time.Date(2026, 11, 19, 23, 3, 18, 148343000, time.UTC))
	if resText(t, res, "host.name") != "nas" || resText(t, res, "service.name") != "sshd" {
		t.Fatalf("resource = %v", res.Attributes().AsRaw())
	}
	attrInt(t, lr.Attributes(), "process.pid", 12)
	if attrStr(t, lr.Attributes(), "log.record.original") != wrapPlainIso {
		t.Fatalf("attributes = %v", lr.Attributes().AsRaw())
	}
}

func TestSyslogUnwrap_IsoTimeWithoutAHostAndTag_IsNotClaimed(t *testing.T) {
	const line = "2026-11-20T00:03:18.148343+01:00 something happened"

	lr, _ := runW4(t, input{body: line})

	w4Untouched(t, lr, line, map[string]any{})
}
