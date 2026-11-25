// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"reflect"
	"testing"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
)

// The sshd and cron lines are captured journald text with the user names and addresses replaced by
// documentation-range values. The systemd lines are invented from the message format.

// journalKeyed are the branches that claim a journald map body by its syslog identifier, so a bare text
// line cannot reach them and the text and keyed sample files hold none for them.
var journalKeyed = map[string]bool{"sshd": true, "systemd": true}

func TestGate_JournalIdentifier_AdmitsOnlyTheNamedMapBodies(t *testing.T) {
	cfg := defaultConfig()
	for name, ident := range map[string]string{"sshd": "sshd", "systemd": "systemd", "cron": "CRON"} {
		g := gateFor(name, cfg)
		mapRec := newRec(plog.NewLogRecord(), pcommon.NewResource(), "text")
		mapRec.lr.Body().SetEmptyMap().PutStr(journalIdentifier, ident)
		other := newRec(plog.NewLogRecord(), pcommon.NewResource(), "text")
		other.lr.Body().SetEmptyMap().PutStr(journalIdentifier, "kernel")
		if !g.admits(mapRec, &seen{}) || g.admits(other, &seen{}) || g.admits(newRec(plog.NewLogRecord(), pcommon.NewResource(), "text"), &seen{}) {
			t.Errorf("%s gate: map of its identifier, other map or text line admitted wrongly", name)
		}
	}
}

func linuxJournal(ident, text string) map[string]any {
	m := journaldMap("MESSAGE", text)
	m["SYSLOG_IDENTIFIER"] = ident
	return m
}

func TestSSHD_AcceptedLogin_GivesUserAddressPortAndInfo(t *testing.T) {
	const line = "Accepted publickey for deploy from 192.0.2.10 port 51234 ssh2: ED25519 SHA256:abcdef"

	lr, res := runW4(t, input{bodyMap: linuxJournal("sshd", line)})

	if got := bodyMapOf(t, lr)["MESSAGE"]; got != line {
		t.Fatalf("MESSAGE = %v", got)
	}
	a := lr.Attributes()
	if attrStr(t, a, "user.name") != "deploy" || attrStr(t, a, "client.address") != "192.0.2.10" {
		t.Fatalf("attributes = %v", a.AsRaw())
	}
	attrInt(t, a, "client.port", 51234)
	if attrStr(t, res.Attributes(), "service.name") != "sshd" || attrStr(t, a, "log.record.original") != line {
		t.Fatalf("resource = %v attributes = %v", res.Attributes().AsRaw(), a.AsRaw())
	}
	w4Severity(t, lr, plog.SeverityNumberInfo, "INFO")
}

func TestSSHD_FailedLogin_GivesTheInvalidUserAndWarn(t *testing.T) {
	lr, _ := runW4(t, input{bodyMap: linuxJournal("sshd", "Failed password for invalid user admin from 192.0.2.77 port 40022 ssh2")})

	a := lr.Attributes()
	if attrStr(t, a, "user.name") != "admin" || attrStr(t, a, "client.address") != "192.0.2.77" {
		t.Fatalf("attributes = %v", a.AsRaw())
	}
	attrInt(t, a, "client.port", 40022)
	w4Severity(t, lr, plog.SeverityNumberWarn, "WARN")
	if _, has := bodyMapOf(t, lr)["PRIORITY"]; has {
		t.Fatal("PRIORITY stayed after the severity changed")
	}
}

func TestSSHD_ClosedByPeer_GivesTheAddressAndPort(t *testing.T) {
	lr, _ := runW4(t, input{bodyMap: linuxJournal("sshd", "Connection closed by 192.0.2.84 port 30727")})

	a := lr.Attributes()
	if attrStr(t, a, "client.address") != "192.0.2.84" {
		t.Fatalf("attributes = %v", a.AsRaw())
	}
	attrInt(t, a, "client.port", 30727)
	absent(t, a, "user.name")
}

func TestSSHD_DisconnectLines_GiveTheAddressNotTheWordUser(t *testing.T) {
	for line, user := range map[string]string{
		"Received disconnect from 192.0.2.39 port 54407:11: disconnected by user": "",
		"Disconnected from user deploy 192.0.2.39 port 54407":                     "deploy",
	} {
		lr, _ := runW4(t, input{bodyMap: linuxJournal("sshd", line)})

		a := lr.Attributes()
		if attrStr(t, a, "client.address") != "192.0.2.39" {
			t.Fatalf("%q: attributes = %v", line, a.AsRaw())
		}
		attrInt(t, a, "client.port", 54407)
		if user == "" {
			absent(t, a, "user.name")
		} else if attrStr(t, a, "user.name") != user {
			t.Fatalf("%q: attributes = %v", line, a.AsRaw())
		}
	}
}

func TestSSHD_PidPrefix_IsStripped(t *testing.T) {
	const line = "sshd[1201]: Failed password for root from 192.0.2.77 port 40022 ssh2"

	lr, _ := runW4(t, input{bodyMap: linuxJournal("sshd", line)})

	if got := bodyMapOf(t, lr)["MESSAGE"]; got != "Failed password for root from 192.0.2.77 port 40022 ssh2" {
		t.Fatalf("MESSAGE = %v", got)
	}
	if attrStr(t, lr.Attributes(), "log.record.original") != line {
		t.Fatalf("attributes = %v", lr.Attributes().AsRaw())
	}
}

func TestSSHD_RefusedLineWithoutUserOrPort_SetsOnlyTheAddress(t *testing.T) {
	lr, _ := runW4(t, input{bodyMap: linuxJournal("sshd", "refused connect from 192.0.2.5 (192.0.2.5)")})

	a := lr.Attributes()
	if attrStr(t, a, "client.address") != "192.0.2.5" {
		t.Fatalf("attributes = %v", a.AsRaw())
	}
	absent(t, a, "user.name", "client.port")
	w4Severity(t, lr, plog.SeverityNumberWarn, "WARN")
}

func TestSSHD_SessionLine_IsInfoWithTheUser(t *testing.T) {
	lr, _ := runW4(t, input{bodyMap: linuxJournal("sshd", "pam_unix(sshd:session): session opened for user deploy(uid=1000) by deploy(uid=0)")})

	if attrStr(t, lr.Attributes(), "user.name") != "deploy" {
		t.Fatalf("attributes = %v", lr.Attributes().AsRaw())
	}
	absent(t, lr.Attributes(), "client.address", "client.port")
	w4Severity(t, lr, plog.SeverityNumberInfo, "INFO")
}

func TestSSHD_OtherLine_KeepsTheSeverityAndPriority(t *testing.T) {
	lr, _ := runW4(t, input{bodyMap: linuxJournal("sshd", "Server listening on 0.0.0.0 port 22.")})

	w4Severity(t, lr, plog.SeverityNumberUnspecified, "")
	if got := bodyMapOf(t, lr)["PRIORITY"]; got != "6" {
		t.Fatalf("PRIORITY = %v, want it kept", got)
	}
}

func TestCron_UntaggedRecord_GivesTheCronServiceAndKeepsTheText(t *testing.T) {
	const line = "(deploy) CMD (command -v debian-sa1 > /dev/null && debian-sa1 1 1)"

	lr, res := runW4(t, input{bodyMap: linuxJournal("CRON", line)})

	if got := bodyMapOf(t, lr)["MESSAGE"]; got != line {
		t.Fatalf("MESSAGE = %v", got)
	}
	if attrStr(t, res.Attributes(), "service.name") != "cron" || attrStr(t, lr.Attributes(), "log.record.original") != line {
		t.Fatalf("resource = %v attributes = %v", res.Attributes().AsRaw(), lr.Attributes().AsRaw())
	}
}

func TestCron_TaggedRecordOfTheCronIdentifier_StripsTheTag(t *testing.T) {
	lr, res := runW4(t, input{bodyMap: linuxJournal("CRON", "cron: (/etc/cron.hourly) starting 0anacron")})

	if got := bodyMapOf(t, lr)["MESSAGE"]; got != "(/etc/cron.hourly) starting 0anacron" {
		t.Fatalf("MESSAGE = %v", got)
	}
	if attrStr(t, res.Attributes(), "service.name") != "cron" {
		t.Fatalf("resource = %v", res.Attributes().AsRaw())
	}
}

func TestSystemd_StartedUnit_LeavesTheSeverityUnset(t *testing.T) {
	const line = "Started Daily apt download activities."

	lr, res := runW4(t, input{bodyMap: linuxJournal("systemd", line)})

	if got := bodyMapOf(t, lr)["MESSAGE"]; got != line {
		t.Fatalf("MESSAGE = %v", got)
	}
	if attrStr(t, res.Attributes(), "service.name") != "systemd" {
		t.Fatalf("resource = %v", res.Attributes().AsRaw())
	}
	w4Severity(t, lr, plog.SeverityNumberUnspecified, "")
}

func TestSystemd_UnitFailure_IsError(t *testing.T) {
	for _, line := range []string{
		"Failed to start Daily apt upgrade and clean activities.",
		"apt-daily.service: Failed with result 'exit-code'.",
	} {
		lr, _ := runW4(t, input{bodyMap: linuxJournal("systemd", line)})

		w4Severity(t, lr, plog.SeverityNumberError, "ERROR")
		if got := bodyMapOf(t, lr)["MESSAGE"]; got != line {
			t.Fatalf("MESSAGE = %v", got)
		}
	}
}

func TestLinuxJournal_NoIdentifier_IsClaimedByNone(t *testing.T) {
	h := newHarness(t, defaultConfig())
	m := linuxJournal("sshd", "Failed password for root from 192.0.2.77 port 40022 ssh2")
	delete(m, "SYSLOG_IDENTIFIER")

	lr, res := h.run(input{bodyMap: m})

	if !reflect.DeepEqual(bodyMapOf(t, lr), m) {
		t.Fatalf("body = %v, want it unchanged", bodyMapOf(t, lr))
	}
	absent(t, lr.Attributes(), "user.name", "client.address", "client.port")
	absent(t, res.Attributes(), "service.name")
	w4Severity(t, lr, plog.SeverityNumberUnspecified, "")
}

func TestLinuxJournal_OtherIdentifier_IsNotClaimed(t *testing.T) {
	m := linuxJournal("chronyd", "Failed to activate service 'org.example': timed out")

	lr, _ := runW4(t, input{bodyMap: m})

	if !reflect.DeepEqual(bodyMapOf(t, lr), m) {
		t.Fatalf("body = %v, want it unchanged", bodyMapOf(t, lr))
	}
}

func TestLinuxJournal_StringBodyWithTheText_IsNotClaimed(t *testing.T) {
	const line = "Failed password for root from 192.0.2.77 port 40022 ssh2"

	lr, _ := runW4(t, input{body: line})

	w4Untouched(t, lr, line, map[string]any{})
}
