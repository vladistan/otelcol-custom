// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
)

// The TLS line is a captured daemon line with the address replaced by a documentation-range one. The
// other line is invented.
const (
	pbsTLSLine   = "[[::ffff:192.0.2.10]:38835] failed to check for TLS handshake: couldn't peek into incoming TCP stream"
	pbsTLSBody   = "failed to check for TLS handshake: couldn't peek into incoming TCP stream"
	pbsErrorLine = "request failed with error: datastore store1 is not available"
)

func pbsDaemon(ident, pid, text string) map[string]any {
	m := linuxJournal(ident, text)
	if pid != "" {
		m["_PID"] = pid
	} else {
		delete(m, "_PID")
	}
	return m
}

func TestPBSDaemon_TLSLine_MovesThePeerOutOfTheBodyAndKeepsTheSeverity(t *testing.T) {
	m := pbsDaemon("proxmox-backup-proxy", "1234", pbsTLSLine)
	m["PRIORITY"] = "3"

	lr, res := runW4(t, input{bodyMap: m})

	if got := bodyMapOf(t, lr)["MESSAGE"]; got != pbsTLSBody {
		t.Fatalf("MESSAGE = %v", got)
	}
	a := lr.Attributes()
	attrInt(t, a, "process.pid", 1234)
	attrInt(t, a, "client.port", 38835)
	if attrStr(t, a, "client.address") != "192.0.2.10" {
		t.Fatalf("attributes = %v", a.AsRaw())
	}
	if attrStr(t, res.Attributes(), "service.name") != "proxmox-backup-proxy" || attrStr(t, a, "log.record.original") != pbsTLSLine {
		t.Fatalf("resource = %v attributes = %v", res.Attributes().AsRaw(), a.AsRaw())
	}
	w4Severity(t, lr, plog.SeverityNumberUnspecified, "")
	if got := bodyMapOf(t, lr)["PRIORITY"]; got != "3" {
		t.Fatalf("PRIORITY = %v, want it kept", got)
	}
}

func TestPBSDaemon_PeerOfAnotherShape_StaysInTheBody(t *testing.T) {
	const line = "[192.0.2.10:443] failed to check for TLS handshake: reset"

	lr, _ := runW4(t, input{bodyMap: pbsDaemon("proxmox-backup-proxy", "1234", line)})

	if got := bodyMapOf(t, lr)["MESSAGE"]; got != line {
		t.Fatalf("MESSAGE = %v", got)
	}
	absent(t, lr.Attributes(), "client.address", "client.port")
}

func TestPBSDaemon_NoPid_LeavesProcessPidUnset(t *testing.T) {
	lr, _ := runW4(t, input{bodyMap: pbsDaemon("proxmox-backup-api", "", pbsTLSLine)})

	absent(t, lr.Attributes(), "process.pid")
	if attrStr(t, lr.Attributes(), "log.record.original") != pbsTLSLine {
		t.Fatalf("attributes = %v", lr.Attributes().AsRaw())
	}
}

func TestPBSDaemon_TextNamingError_IsError(t *testing.T) {
	lr, _ := runW4(t, input{bodyMap: pbsDaemon("proxmox-backup-proxy", "1234", pbsErrorLine)})

	w4Severity(t, lr, plog.SeverityNumberError, "ERROR")
	if _, has := bodyMapOf(t, lr)["PRIORITY"]; has {
		t.Fatal("PRIORITY stayed after the severity changed")
	}
}

func TestPBSDaemon_OtherIdentifier_IsNotClaimed(t *testing.T) {
	for _, ident := range []string{"proxmox-backup-client", "kernel"} {
		m := pbsDaemon(ident, "1234", pbsTLSLine)

		lr, res := runW4(t, input{bodyMap: m})

		if !reflect.DeepEqual(bodyMapOf(t, lr), m) {
			t.Fatalf("%s: body = %v, want it unchanged", ident, bodyMapOf(t, lr))
		}
		absent(t, res.Attributes(), "service.name")
	}
}

func TestPBSDaemon_TaskLogFileNameWinsOverTheIdentifier(t *testing.T) {
	m := pbsDaemon("proxmox-backup-proxy", "1234", "2026-11-20T00:02:45+01:00: processed 99% (583303 chunks)")

	lr, res := runW4(t, input{bodyMap: m, attrs: map[string]any{"log.file.name": pbsGC}})

	if attrStr(t, lr.Attributes(), "vstan.pbs.task") != "garbage_collection" || attrStr(t, res.Attributes(), "service.name") != "garbage_collection" {
		t.Fatalf("resource = %v attributes = %v", res.Attributes().AsRaw(), lr.Attributes().AsRaw())
	}
}

func TestGate_PBS_AdmitsTheDaemonIdentifiersAndTaskLogFiles(t *testing.T) {
	g := gateFor("pbs", defaultConfig())
	for _, ident := range []string{"proxmox-backup-proxy", "proxmox-backup-api", "proxmox-backup"} {
		r := newRec(plog.NewLogRecord(), pcommon.NewResource(), "text")
		r.lr.Body().SetEmptyMap().PutStr(journalIdentifier, ident)
		if !g.admits(r, &seen{}) {
			t.Errorf("gate refuses the identifier %s", ident)
		}
	}
	other := newRec(plog.NewLogRecord(), pcommon.NewResource(), "text")
	other.lr.Body().SetEmptyMap().PutStr(journalIdentifier, "kernel")
	if g.admits(other, &seen{}) {
		t.Error("gate admits another identifier")
	}
	file := newRec(plog.NewLogRecord(), pcommon.NewResource(), "2026-11-20T00:02:45+01:00: x")
	file.lr.Attributes().PutStr("log.file.name", pbsGC)
	if !g.admits(file, &seen{}) {
		t.Error("gate refuses a task log file")
	}
}

type pbsSample struct {
	Identifier string `json:"identifier"`
	PID        string `json:"pid"`
	Text       string `json:"text"`
}

func TestPBSDaemon_SampleFile_EveryRecordIsShaped(t *testing.T) {
	raw, err := os.ReadFile("testdata/samples-journal/pbs-daemon.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(raw), "\n")
	if lines[0] != "# provenance: real" {
		t.Fatalf("first line = %q, want the real provenance header", lines[0])
	}
	n := 0
	for _, line := range lines {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		var s pbsSample
		if err := json.Unmarshal([]byte(line), &s); err != nil {
			t.Fatal(err)
		}
		lr, res := runW4(t, input{bodyMap: pbsDaemon(s.Identifier, s.PID, s.Text)})
		if attrStr(t, res.Attributes(), "service.name") != s.Identifier || attrStr(t, lr.Attributes(), "log.record.original") != s.Text {
			t.Errorf("%s: resource = %v attributes = %v", s.Text, res.Attributes().AsRaw(), lr.Attributes().AsRaw())
		}
		n++
	}
	if n < 2 {
		t.Fatalf("%d records, want at least 2", n)
	}
}
