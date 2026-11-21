// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

// cspell:ignore vstan

package logshapeprocessor

import (
	"os"
	"path/filepath"
	"testing"

	"go.opentelemetry.io/collector/pdata/plog"
)

// ciscoInput is a record as the udp receiver leaves it: the raw line in log.record.original and the peer name.
func ciscoInput(original, peer, body string) input {
	attrs := map[string]any{}
	if original != "" {
		attrs["log.record.original"] = original
	}
	if peer != "" {
		attrs["net.peer.name"] = peer
	}
	return input{body: body, attrs: attrs}
}

func TestCisco_WLCLine_SetsTheAttributesTheHostAndTheCleanMessage(t *testing.T) {
	h := newHarness(t, defaultConfig())
	in := ciscoInput("DownAP-Master: *apfMsConnTask_0: Dec 16 00:00:44.695: %APF-5-CLIENT_ASSOCIATE: apf_80211.c:13338 Client Association: Client MAC: c4:4f:33:91:a6:58, AP Name: CiscoAP-Down, Radio: 2.4GHz , WLAN Id: 1.",
		"wifi-ap.home.example.com", "<133>DownAP-Master: message")

	lr, res := h.run(in)

	a := lr.Attributes()
	for k, want := range map[string]string{
		"vstan.cisco.facility": "APF", "vstan.cisco.mnemonic": "CLIENT_ASSOCIATE", "vstan.cisco.severity": "5",
		"vstan.wifi.ap.name": "DownAP-Master", "thread.name": "apfMsConnTask_0", "code.file.path": "apf_80211.c",
		"vstan.wifi.client.ap": "CiscoAP-Down", "vstan.wifi.radio": "2.4GHz", "vstan.wifi.wlan.id": "1",
	} {
		if got := attrStr(t, a, k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	attrInt(t, a, "code.line.number", 13338)
	absent(t, a, "code.filepath", "code.lineno")
	if attrStr(t, res.Attributes(), "host.name") != "wifi-ap.home.example.com" || attrStr(t, res.Attributes(), "service.name") != "APF" {
		t.Errorf("resource = %v", res.Attributes().AsRaw())
	}
	if got := lr.Body().Str(); got != "Client Association" {
		t.Errorf("body = %q", got)
	}
	if lr.SeverityNumber() != plog.SeverityNumberInfo || lr.SeverityText() != "INFO" {
		t.Errorf("severity = %d %q", lr.SeverityNumber(), lr.SeverityText())
	}
}

func TestCisco_AlreadyParsedRecord_IsCleanedAndKeepsItsAttributes(t *testing.T) {
	h := newHarness(t, defaultConfig())
	in := input{
		body:     "<189>body content",
		attrs:    map[string]any{"cisco_facility": "SW_MATM", "cisco_mnemonic": "MACFLAP_NOTIF"},
		resource: map[string]any{"host.name": "switch1", "service.name": "SW_MATM"},
	}

	lr, res := h.run(in)

	if lr.Body().Str() != "body content" {
		t.Errorf("body = %q", lr.Body().Str())
	}
	if attrStr(t, lr.Attributes(), "vstan.cisco.facility") != "SW_MATM" {
		t.Errorf("attributes = %v", lr.Attributes().AsRaw())
	}
	if attrStr(t, res.Attributes(), "host.name") != "switch1" {
		t.Errorf("resource = %v", res.Attributes().AsRaw())
	}
	if lr.SeverityNumber() != plog.SeverityNumberWarn {
		t.Errorf("MACFLAP_NOTIF severity = %d, want WARN", lr.SeverityNumber())
	}
}

func TestCisco_HPLine_SetsHostServiceAndTheCleanMessage(t *testing.T) {
	h := newHarness(t, defaultConfig())
	in := ciscoInput(" Dec 15 21:31:30 switch-c-1 General[tRpcsrv.00001]: usmdb_sim.c(3847) 2805 %% Event(0x0)", "",
		"<10> Dec 15 21:31:30 switch-c-1 General[tRpcsrv.00001]: message")

	lr, res := h.run(in)

	if attrStr(t, res.Attributes(), "host.name") != "switch-c-1" || attrStr(t, res.Attributes(), "service.name") != "General" {
		t.Errorf("resource = %v", res.Attributes().AsRaw())
	}
	a := lr.Attributes()
	if attrStr(t, a, "thread.name") != "tRpcsrv.00001" || attrStr(t, a, "code.file.path") != "usmdb_sim.c" || attrStr(t, a, "vstan.cisco.sequence") != "2805" {
		t.Errorf("attributes = %v", a.AsRaw())
	}
	attrInt(t, a, "code.line.number", 3847)
	if lr.Body().Str() != "Event(0x0)" {
		t.Errorf("body = %q", lr.Body().Str())
	}
}

func TestCisco_HPLineWithCodeLocation_KeepsTheSequenceAndTheProcess(t *testing.T) {
	h := newHarness(t, defaultConfig())
	in := ciscoInput(" Dec 15 22:56:14 switch-c-1 TRAPMGR[dot1s_task]: traputil.c(763) 284 %% Spanning Tree Topology Change Received", "",
		"<10> Dec 15 22:56:14 switch-c-1 TRAPMGR[dot1s_task]: message")

	lr, _ := h.run(in)

	a := lr.Attributes()
	if attrStr(t, a, "code.file.path") != "traputil.c" || attrStr(t, a, "vstan.cisco.sequence") != "284" || attrStr(t, a, "thread.name") != "dot1s_task" {
		t.Errorf("attributes = %v", a.AsRaw())
	}
	attrInt(t, a, "code.line.number", 763)
	if lr.Body().Str() != "Spanning Tree Topology Change Received" {
		t.Errorf("body = %q", lr.Body().Str())
	}
}

func TestCisco_BareMnemonic_GivesTheHostFromThePeerAndTheCleanBody(t *testing.T) {
	h := newHarness(t, defaultConfig())
	in := ciscoInput("%COPY-N-TRAP: The copy operation was completed successfully", "switch-a.home.example.com",
		"<189>%COPY-N-TRAP: The copy operation was completed successfully")

	lr, res := h.run(in)

	a := lr.Attributes()
	if attrStr(t, a, "vstan.cisco.facility") != "COPY" || attrStr(t, a, "vstan.cisco.mnemonic") != "TRAP" || attrStr(t, a, "vstan.cisco.severity") != "N" {
		t.Errorf("attributes = %v", a.AsRaw())
	}
	if attrStr(t, res.Attributes(), "host.name") != "switch-a.home.example.com" {
		t.Errorf("resource = %v", res.Attributes().AsRaw())
	}
	if lr.Body().Str() != "%COPY-N-TRAP: The copy operation was completed successfully" {
		t.Errorf("body = %q", lr.Body().Str())
	}
	if lr.SeverityText() != "INFO" {
		t.Errorf("severity = %q, want INFO for a notice", lr.SeverityText())
	}
}

func TestCisco_MnemonicOnlyInTheBody_IsFound(t *testing.T) {
	h := newHarness(t, defaultConfig())

	lr, _ := h.run(input{body: "%LINK-3-UPDOWN: Interface GigabitEthernet0/1, changed state to down"})

	if attrStr(t, lr.Attributes(), "vstan.cisco.facility") != "LINK" {
		t.Errorf("attributes = %v", lr.Attributes().AsRaw())
	}
	if lr.SeverityNumber() != plog.SeverityNumberWarn || lr.SeverityText() != "WARN" {
		t.Errorf("an interface state change must be WARN, got %d %q", lr.SeverityNumber(), lr.SeverityText())
	}
	if attrStr(t, lr.Attributes(), "network.interface.name") != "GigabitEthernet0/1" {
		t.Errorf("attributes = %v", lr.Attributes().AsRaw())
	}
}

func TestCisco_MacFlap_TakesTheFirstInterfaceAndWarn(t *testing.T) {
	h := newHarness(t, defaultConfig())
	in := ciscoInput("661: *Dec 16 00:36:36.024: %SW_MATM-4-MACFLAP_NOTIF: Host 6212.24a7.dd56 in vlan 1 is flapping between port Gi4/0/45 and port Gi4/0/43", "",
		"Host flapping between ports")

	lr, _ := h.run(in)

	if attrStr(t, lr.Attributes(), "network.interface.name") != "Gi4/0/45" {
		t.Errorf("attributes = %v", lr.Attributes().AsRaw())
	}
	if lr.SeverityNumber() != plog.SeverityNumberWarn {
		t.Errorf("severity = %d", lr.SeverityNumber())
	}
}

func TestCisco_NotACiscoLine_IsNotClaimed(t *testing.T) {
	h := newHarness(t, defaultConfig())
	const body = "<134>Oct 15 09:23:45 server sshd[1234]: Accepted password"

	lr, res := h.run(ciscoInput("Oct 15 09:23:45 server sshd[1234]: Accepted password", "server.example.com", body))

	absent(t, lr.Attributes(), "vstan.cisco.facility")
	if lr.Body().Str() != body {
		t.Errorf("body changed to %q", lr.Body().Str())
	}
	if _, ok := res.Attributes().Get("host.name"); ok {
		t.Errorf("host set on a record no branch claimed: %v", res.Attributes().AsRaw())
	}
	if got := h.counter(unmatchedMetric, "source"); got["none"] != 1 {
		t.Errorf("unmatched = %v", got)
	}
}

func TestCisco_TwoPeers_GetTheirOwnResources(t *testing.T) {
	h := newHarness(t, defaultConfig())
	ld := plog.NewLogs()
	sl := ld.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty()
	for _, peer := range []string{"sw-a", "sw-b", "sw-a"} {
		lr := sl.LogRecords().AppendEmpty()
		lr.Attributes().PutStr("log.record.original", "%SYS-5-CONFIG_I: Configured from console")
		lr.Attributes().PutStr("net.peer.name", peer)
		lr.Body().SetStr("x")
	}

	out := h.runBatch(ld)

	if out.ResourceLogs().Len() != 2 {
		t.Fatalf("%d resources, want 2", out.ResourceLogs().Len())
	}
	counts := map[string]int{}
	for _, f := range flatten(out) {
		counts[f.host]++
	}
	if counts["sw-a"] != 2 || counts["sw-b"] != 1 {
		t.Errorf("records by host = %v", counts)
	}
}

func TestCisco_PortDatabase_AddsTheInterfaceAlias(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ports.yaml")
	const db = "ports:\n  switch-a.home.example.com:\n    Gi4/0/45: uplink to the core\n"
	if err := os.WriteFile(path, []byte(db), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := defaultConfig()
	cfg.Cisco.PortDatabasePath = path
	h := newHarness(t, cfg)
	in := ciscoInput("661: *Dec 16 00:36:36.024: %SW_MATM-4-MACFLAP_NOTIF: flapping between port Gi4/0/45 and port Gi4/0/43",
		"switch-a.home.example.com", "x")

	lr, _ := h.run(in)

	if got := attrStr(t, lr.Attributes(), "vstan.cisco.interface_alias"); got != "uplink to the core" {
		t.Errorf("alias = %q", got)
	}
}

func TestCisco_PortDatabaseThatCannotBeRead_FailsAtStartup(t *testing.T) {
	cfg := defaultConfig()
	cfg.Cisco.PortDatabasePath = filepath.Join(t.TempDir(), "missing.yaml")

	if _, err := newProcessor(newSettings(), cfg, nopConsumer()); err == nil {
		t.Fatal("a port database that cannot be read must fail startup")
	}
}

func TestCisco_RewriteOff_KeepsTheBodyApartFromThePriorityPrefix(t *testing.T) {
	cfg := defaultConfig()
	cfg.Cisco.RewriteMessages = false
	h := newHarness(t, cfg)
	in := ciscoInput("DownAP-Master: *apfMsConnTask_0: Dec 15 20:49:14.582: %APF-5-CLIENT_ASSOCIATE: Client Association", "wifi-ap.home.example.com",
		"<133>DownAP-Master: message")

	lr, _ := h.run(in)

	if lr.Body().Str() != "DownAP-Master: message" {
		t.Errorf("body = %q", lr.Body().Str())
	}
}

func TestCisco_AdjustSeverityOff_KeepsTheCiscoLevel(t *testing.T) {
	cfg := defaultConfig()
	cfg.Cisco.AdjustSeverity = false
	h := newHarness(t, cfg)

	lr, _ := h.run(input{body: "%LINK-3-UPDOWN: Interface GigabitEthernet0/1, changed state to down"})

	if lr.SeverityNumber() != plog.SeverityNumberUnspecified {
		t.Errorf("severity = %d, want unset", lr.SeverityNumber())
	}
}
