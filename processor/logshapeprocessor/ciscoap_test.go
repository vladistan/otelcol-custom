// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

// cspell:ignore pmkr ciscoap clsm hostapd

package logshapeprocessor

import (
	"testing"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
)

// REAL lines of a Cisco access point, as the syslog receiver stored them at priority 4 (WARN): a
// kernel message behind a device clock "[*MM/DD/YYYY HH:MM:SS.ffff]" in local time. Only the MAC
// and the host name are replaced. The branch is cisco.
const (
	apRoutine = "[*11/20/2026 06:11:26.3538] hostapd:FT: Skip PMKR0 lookup"
	apRoaming = "[*11/19/2026 20:56:46.0939] CLSM[00:11:22:33:44:55]: DELETE_MOBILE payload is ignored - client roamed to other radio in same AP"
	apNotable = "[*11/19/2026 22:50:37.7639] DOT11_DRV[0]: Stop Radio0 - Begin"
	apWrapped = "Nov 20 06:11:26 kernel: " + apRoutine
)

// runAP sends one record at the given severity, as a syslog priority 4 gives, through a processor with cfg.
func runAP(t *testing.T, cfg *Config, in input, num plog.SeverityNumber) (plog.LogRecord, pcommon.Resource) {
	t.Helper()
	h := newHarness(t, cfg)
	ld := in.logs()
	lr := ld.ResourceLogs().At(0).ScopeLogs().At(0).LogRecords().At(0)
	lr.SetSeverityNumber(num)
	lr.SetSeverityText("WARN")
	if err := h.proc.ConsumeLogs(t.Context(), ld); err != nil {
		t.Fatal(err)
	}
	out := h.sink.AllLogs()[0].ResourceLogs().At(0)
	return out.ScopeLogs().At(0).LogRecords().At(0), out.Resource()
}

func TestCiscoAP_RoutineKernelLine_LosesTheClockAndIsDowngraded(t *testing.T) {
	lr, _ := runAP(t, defaultConfig(), input{body: apRoutine}, plog.SeverityNumberWarn)

	w4Body(t, lr, "hostapd:FT: Skip PMKR0 lookup")
	w4Severity(t, lr, plog.SeverityNumberInfo, "INFO")
	if attrStr(t, lr.Attributes(), "log.record.original") != apRoutine {
		t.Fatalf("attributes = %v", lr.Attributes().AsRaw())
	}
}

func TestCiscoAP_ClientRoamedLine_LosesTheClockAndIsDowngraded(t *testing.T) {
	lr, _ := runAP(t, defaultConfig(), input{body: apRoaming}, plog.SeverityNumberWarn)

	w4Body(t, lr, "CLSM[00:11:22:33:44:55]: DELETE_MOBILE payload is ignored - client roamed to other radio in same AP")
	w4Severity(t, lr, plog.SeverityNumberInfo, "INFO")
}

func TestCiscoAP_NotableRadioMessage_KeepsTheDeviceWarn(t *testing.T) {
	lr, _ := runAP(t, defaultConfig(), input{body: apNotable}, plog.SeverityNumberWarn)

	w4Body(t, lr, "DOT11_DRV[0]: Stop Radio0 - Begin")
	w4Severity(t, lr, plog.SeverityNumberWarn, "WARN")
}

func TestCiscoAP_ListSetInConfig_ReplacesTheDefault(t *testing.T) {
	cfg := defaultConfig()
	cfg.Cisco.AccessPointDowngrade = []string{"stop radio0"}

	routine, _ := runAP(t, cfg, input{body: apRoutine}, plog.SeverityNumberWarn)
	listed, _ := runAP(t, cfg, input{body: apNotable}, plog.SeverityNumberWarn)

	w4Severity(t, routine, plog.SeverityNumberWarn, "WARN")
	w4Severity(t, listed, plog.SeverityNumberInfo, "INFO")
}

func TestCiscoAP_LineBehindASyslogWrapper_GivesServiceHostAndTheDowngrade(t *testing.T) {
	in := input{body: apWrapped, attrs: map[string]any{"priority": "4", "net.peer.name": "ap-host.example.net"}}

	lr, res := runAP(t, defaultConfig(), in, plog.SeverityNumberWarn)

	w4Body(t, lr, "hostapd:FT: Skip PMKR0 lookup")
	w4Severity(t, lr, plog.SeverityNumberInfo, "INFO")
	if resText(t, res, "service.name") != "kernel" || resText(t, res, "host.name") != "ap-host.example.net" {
		t.Fatalf("resource = %v", res.Attributes().AsRaw())
	}
}

func TestCiscoAP_TextWithoutADeviceClock_IsNotClaimed(t *testing.T) {
	const line = "[info] the build finished"

	lr, _ := runW4(t, input{body: line})

	w4Untouched(t, lr, line, map[string]any{})
}
