// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

// cspell:ignore vstan IOAV

package winlogprocessor

import (
	"strings"
	"testing"

	"go.opentelemetry.io/collector/pdata/plog"
)

const (
	defenderNever = "4294967295"
	defenderBase  = "Defender product status 0x00080000, RTP/OA/IOAV/BM Enabled, definitions age 0 d, last quick scan 4 d ago"
)

// realDefender1151 has the shape of a captured Defender 1151 event: the event
// data has one key per body line, and the key names contain spaces. Names and
// the host are neutral. quickAge is the value of "Last quick scan age" and rtp
// the value of "RTP state"; the real row had 4 and Enabled. The age keys are
// "Last AV security intelligence age" and "Last AS security intelligence age",
// although the body labels say Antivirus and Antispyware.
func realDefender1151(quickAge, rtp string) receiverEvent {
	data := map[string]any{
		"Platform version":                  "4.18.26100.4",
		"Engine version":                    "1.1.26100.4",
		"AV security intelligence version":  "1.463.380.0",
		"RTP state":                         rtp,
		"OA state":                          "Enabled",
		"IOAV state":                        "Enabled",
		"BM state":                          "Enabled",
		"Last AV security intelligence age": "0",
		"Last AS security intelligence age": "0",
		"Last quick scan age":               quickAge,
		"Last full scan age":                defenderNever,
		"Last quick scan start time":        "2026-11-16T03:58:26Z",
		"Last full scan end time":           "1601-01-01T00:00:00Z",
		"Engine up-to-date":                 "0",
		"Platform up-to-date":               "1",
		"Product Name":                      "Microsoft Defender Antivirus",
		"Product status":                    "0x00080000",
		"Unused":                            "",
	}
	lines := []string{
		"Endpoint Protection client health report (time in UTC):",
		" \tPlatform version: 4.18.26100.4",
		" \tEngine version: 1.1.26100.4",
		" \tNetwork Realtime Inspection engine version: 1.1.26100.4",
		" \tAntivirus security intelligence version: 1.463.380.0",
		" \tRTP state: " + rtp,
		" \tOA state: Enabled",
		" \tIOAV state: Enabled",
		" \tBM state: Enabled",
		" \tAntivirus security intelligence age: 0",
		" \tAntispyware security intelligence age: 0",
		" \tLast quick scan age: " + quickAge,
		" \tLast full scan age: " + defenderNever,
		" \tLast quick scan start time: 2026-11-16T03:58:26Z",
		" \tProduct status: 0x00080000",
	}
	return receiverEvent{
		channel:   "Microsoft-Windows-Windows Defender/Operational",
		provider:  "Microsoft-Windows-Windows Defender",
		computer:  "pc01.example.org",
		id:        1151,
		recordID:  3019,
		level:     "Information",
		task:      "0",
		opcode:    "Info",
		keywords:  []any{"0x8000000000000000"},
		userID:    "S-1-5-18",
		processID: 4456,
		threadID:  7444,
		message:   strings.Join(lines, "\r\n") + "\r\n",
		eventData: data,
	}
}

func TestRealDefender1151SummaryNamesStatusProtectionAndAges(t *testing.T) {
	event := realDefender1151("4", "Enabled")
	record := shapeCfg(t, &Config{}, event)
	if got := record.Body().Str(); got != defenderBase {
		t.Errorf("body = %q, want %q", got, defenderBase)
	}
	if got := attrString(t, record, "vstan.winlog.message"); got != event.message {
		t.Errorf("vstan.winlog.message = %q, want the rendered message", got)
	}
	if got := attrString(t, record, "winlog.event_data.RTP state"); got != "Enabled" {
		t.Errorf("the event data keys with spaces must stay attributes, got %q", got)
	}
	if record.SeverityNumber() != plog.SeverityNumberInfo {
		t.Errorf("severity = %v, want INFO", record.SeverityNumber())
	}
}

func TestRealDefender1151ASentinelAgeIsNever(t *testing.T) {
	record := shapeCfg(t, &Config{}, realDefender1151(defenderNever, "Enabled"))
	want := "Defender product status 0x00080000, RTP/OA/IOAV/BM Enabled, definitions age 0 d, last quick scan never"
	if got := record.Body().Str(); got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
}

func TestRealDefender1151NeitherSentinelNorEpochDateReachesTheSummary(t *testing.T) {
	for _, quick := range []string{"4", defenderNever} {
		got := shapeCfg(t, &Config{}, realDefender1151(quick, "Enabled")).Body().Str()
		for _, bad := range []string{defenderNever, "1601-01-01", "2026-11-16"} {
			if strings.Contains(got, bad) {
				t.Errorf("quick scan age %q: body %q contains %q", quick, got, bad)
			}
		}
	}
}

func TestRealDefender1151ADisabledStateIsListedByName(t *testing.T) {
	record := shapeCfg(t, &Config{}, realDefender1151("4", "Disabled"))
	want := "Defender product status 0x00080000, RTP Disabled, OA Enabled, IOAV Enabled, BM Enabled, definitions age 0 d, last quick scan 4 d ago"
	if got := record.Body().Str(); got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
	if record.SeverityNumber() != plog.SeverityNumberInfo {
		t.Errorf("severity = %v, want INFO", record.SeverityNumber())
	}
}

func TestRealDefender1151LeavesOutWhatTheEventLacks(t *testing.T) {
	event := realDefender1151("4", "Enabled")
	for _, key := range []string{"Last quick scan age", "Last AV security intelligence age", "Last AS security intelligence age", "Product status"} {
		delete(event.eventData, key)
	}
	want := "Defender RTP/OA/IOAV/BM Enabled"
	if got := shapeCfg(t, &Config{}, event).Body().Str(); got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
}

func TestRealDefender1151KeepsTheBodyWhenNothingNeededIsThere(t *testing.T) {
	event := realDefender1151("4", "Enabled")
	for _, key := range []string{
		"Last quick scan age", "Last AV security intelligence age", "Last AS security intelligence age",
		"Product status", "RTP state", "OA state", "IOAV state", "BM state",
	} {
		delete(event.eventData, key)
	}
	record := shapeCfg(t, &Config{}, event)
	if got := record.Body().Str(); got != event.message {
		t.Errorf("body = %q, want the message kept", got)
	}
	if _, ok := record.Attributes().Get("vstan.winlog.message"); ok {
		t.Error("vstan.winlog.message is set although no summary was made")
	}
}

func TestDefender1151NoLongerReadsTheGuessedNames(t *testing.T) {
	event := receiverEvent{
		channel: "Microsoft-Windows-Windows Defender/Operational", provider: "Microsoft-Windows-Windows Defender", id: 1151,
		message: "Endpoint Protection client health report",
		eventData: map[string]any{
			"Real-Time Protection state": "Enabled", "On Access Protection state": "Enabled",
			"Antivirus signature age": "0",
		},
	}
	if got := shapeCfg(t, &Config{}, event).Body().Str(); got != event.message {
		t.Errorf("body = %q, want the message kept: the guessed names are no longer read", got)
	}
}

func TestRealDefender1151FallsBackToTheASAgeWhenTheAVAgeIsAbsent(t *testing.T) {
	event := realDefender1151("4", "Enabled")
	delete(event.eventData, "Last AV security intelligence age")
	event.eventData["Last AS security intelligence age"] = "2"
	want := "Defender product status 0x00080000, RTP/OA/IOAV/BM Enabled, definitions age 2 d, last quick scan 4 d ago"
	if got := shapeCfg(t, &Config{}, event).Body().Str(); got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
}

func TestRealDefender1151TheAVAgeWinsOverTheASAge(t *testing.T) {
	event := realDefender1151("4", "Enabled")
	event.eventData["Last AS security intelligence age"] = "9"
	if got := shapeCfg(t, &Config{}, event).Body().Str(); got != defenderBase {
		t.Errorf("body = %q, want %q", got, defenderBase)
	}
}

func TestRealDefender1151TheBodyLabelsAreNotKeys(t *testing.T) {
	event := realDefender1151("4", "Enabled")
	for _, key := range []string{"Last AV security intelligence age", "Last AS security intelligence age"} {
		delete(event.eventData, key)
	}
	event.eventData["AV security intelligence age"] = "7"
	event.eventData["Antivirus security intelligence age"] = "7"
	want := "Defender product status 0x00080000, RTP/OA/IOAV/BM Enabled, last quick scan 4 d ago"
	if got := shapeCfg(t, &Config{}, event).Body().Str(); got != want {
		t.Errorf("body = %q, want %q: only the confirmed age keys are read", got, want)
	}
}
