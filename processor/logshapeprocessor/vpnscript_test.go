// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"testing"
	"time"

	"go.opentelemetry.io/collector/pdata/plog"
)

// The layout is a script that writes "[timestamp] text" lines. The texts are made up, except vpnRealLines:
// the first lines of the records of a VPN configuration script, with the host name replaced.
var (
	vpnRealLines = map[string]string{
		"[2026-11-23 02:23:53]   Services updated: 0":                        "Services updated: 0",
		"[2026-11-23 02:23:53] Configuration update complete:":               "Configuration update complete:",
		"[2026-11-23 02:23:53]   No changes for unbound, skipping update":    "No changes for unbound, skipping update",
		"[2026-11-23 02:23:52]   No change in /etc/ipsec.secrets":            "No change in /etc/ipsec.secrets",
		"[2026-11-23 02:23:51]   Fetching gw1-ipsec-conf -> /etc/ipsec.conf": "Fetching gw1-ipsec-conf -> /etc/ipsec.conf",
		"[2026-11-23 02:23:51] Processing service: strongswan":               "Processing service: strongswan",
	}

	vpnTime = time.Date(2026, 11, 22, 14, 38, 13, 0, time.UTC)

	vpnForms = map[string]time.Time{
		"[2026-11-22 14:38:13] tunnel up":          vpnTime,
		"[2026-11-22T14:38:13] tunnel up":          vpnTime,
		"[2026-11-22T14:38:13Z] tunnel up":         vpnTime,
		"[2026-11-22T14:38:13+01:00] tunnel up":    vpnTime.Add(-time.Hour),
		"[2026-11-22T14:38:13+0100] tunnel up":     vpnTime.Add(-time.Hour),
		"[2026-11-22 14:38:13.250] tunnel up":      vpnTime.Add(250 * time.Millisecond),
		"[Sun Nov 22 14:38:13 UTC 2026] tunnel up": vpnTime,
		"[Sun Nov 22 14:38:13 2026] tunnel up":     vpnTime,
		"[Sun Nov  1 14:38:13 2026] tunnel up":     time.Date(2026, 11, 1, 14, 38, 13, 0, time.UTC),
		"[Sun Nov 22 14:38:13 PST 2026] tunnel up": vpnTime,
	}
)

func vpnRun(t *testing.T, body string) plog.LogRecord {
	t.Helper()
	lr, _ := runW4(t, input{body: body})
	return lr
}

func TestVPNScript_EveryTimestampForm_IsClaimedWithTheTextAndTheTime(t *testing.T) {
	for line, want := range vpnForms {
		t.Run(line, func(t *testing.T) {
			lr := vpnRun(t, line)

			w4Body(t, lr, "tunnel up")
			w4Time(t, lr, want)
			if got := attrStr(t, lr.Attributes(), "log.record.original"); got != line {
				t.Fatalf("log.record.original = %q", got)
			}
		})
	}
}

func TestVPNScript_RealIndentedLines_GiveTheTrimmedTextTheTimeAndINFO(t *testing.T) {
	for line, text := range vpnRealLines {
		t.Run(text, func(t *testing.T) {
			lr := vpnRun(t, line)

			w4Body(t, lr, text)
			w4Severity(t, lr, plog.SeverityNumberInfo, "INFO")
			if got := lr.Timestamp().AsTime().Format("2006-01-02"); got != "2026-11-23" {
				t.Fatalf("date = %s", got)
			}
			if got := attrStr(t, lr.Attributes(), "log.record.original"); got != line {
				t.Fatalf("log.record.original = %q", got)
			}
		})
	}
}

func TestVPNScript_BracketThatIsNotATimestamp_IsNotClaimed(t *testing.T) {
	for _, line := range []string{
		"[main] tunnel up", "[INFO] tunnel up", "[2026-11-22] tunnel up", "[2026-13-45 99:99:99] tunnel up",
		"[] tunnel up", "tunnel up [2026-11-22 14:38:13]",
	} {
		t.Run(line, func(t *testing.T) {
			lr := vpnRun(t, line)

			w4Untouched(t, lr, line, map[string]any{})
		})
	}
}

func TestVPNScript_NoLevelWord_GivesINFO(t *testing.T) {
	w4Severity(t, vpnRun(t, "[2026-11-22 14:38:13] tunnel up"), plog.SeverityNumberInfo, "INFO")
}

func TestVPNScript_LevelWordInTheText_GivesThatLevel(t *testing.T) {
	for _, c := range []struct {
		text string
		num  plog.SeverityNumber
		word string
	}{
		{"connection error on tun0", plog.SeverityNumberError, "ERROR"},
		{"WARN: slow handshake", plog.SeverityNumberWarn, "WARN"},
		{"warning: slow handshake", plog.SeverityNumberWarn, "WARN"},
		{"fatal: cannot open tun0", plog.SeverityNumberFatal, "FATAL"},
		{"Error then fatal", plog.SeverityNumberFatal, "FATAL"},
	} {
		t.Run(c.text, func(t *testing.T) {
			lr := vpnRun(t, "[2026-11-22 14:38:13] "+c.text)

			w4Severity(t, lr, c.num, c.word)
			w4Body(t, lr, c.text)
		})
	}
}

func TestVPNScript_LevelWordInsideAnotherWord_IsNotALevel(t *testing.T) {
	lr := vpnRun(t, "[2026-11-22 14:38:13] terror of the errors and warnings")

	w4Severity(t, lr, plog.SeverityNumberInfo, "INFO")
}

func TestVPNScript_TrailingCarriageReturnAndLeadingSpaces_AreTrimmedBeforeTheClaim(t *testing.T) {
	lr := vpnRun(t, "   [2026-11-22 14:38:13] tunnel up\r")

	w4Body(t, lr, "tunnel up")
	w4Time(t, lr, vpnTime)
}

func TestVPNScript_EmptyText_GivesAnEmptyBody(t *testing.T) {
	lr := vpnRun(t, "[2026-11-22 14:38:13]")

	w4Body(t, lr, "")
	w4Severity(t, lr, plog.SeverityNumberInfo, "INFO")
}

func TestVPNScript_PinoLineWithATagBracket_KeepsItsBranch(t *testing.T) {
	lr := vpnRun(t, `[JOB-RUNNER] {"level":50,"time":1795129202193,"pid":19,"msg":"fetch failed"}`)

	w4Severity(t, lr, plog.SeverityNumberError, "ERROR")
}

func TestVPNScript_SentryLine_KeepsItsBranch(t *testing.T) {
	lr := vpnRun(t, "02:30:22 [WARNING] sentry.tasks: Retrying the task")

	w4Severity(t, lr, plog.SeverityNumberWarn, "WARN")
	if _, ok := lr.Attributes().Get("log.record.original"); !ok {
		t.Fatalf("sentry did not claim its line: %v", lr.Attributes().AsRaw())
	}
}

func TestVPNScript_Gate_AdmitsATimestampBracketWithLeadingSpaceOrCarriageReturn(t *testing.T) {
	g := gateFor("vpnscript", defaultConfig())
	var s seen

	for line := range vpnForms {
		if !g.admits(&rec{body: line}, &s) {
			t.Errorf("gate refuses %q", line)
		}
	}
	for _, line := range []string{"   [2026-11-22 14:38:13] up", "[2026-11-22 14:38:13] up\r"} {
		if !g.admits(&rec{body: line}, &s) {
			t.Errorf("gate refuses %q", line)
		}
	}
}

func TestVPNScript_Gate_RefusesBodiesThatDoNotStartWithABracket(t *testing.T) {
	g := gateFor("vpnscript", defaultConfig())
	var s seen

	for _, line := range []string{"", "plain text", "2026-11-22 14:38:13 up", "tunnel up [2026-11-22 14:38:13]", "[short]"} {
		if g.admits(&rec{body: line}, &s) {
			t.Errorf("gate admits %q", line)
		}
	}
}
