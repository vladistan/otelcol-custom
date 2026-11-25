// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

// cspell:ignore charon strongswan ipsec

package logshapeprocessor

import (
	"testing"

	"go.opentelemetry.io/collector/pdata/plog"
)

func charonInput(service, body string) input {
	return input{body: body, resource: map[string]any{"service.name": service}}
}

func TestCharon_ClaimsStrongswanStarterWithAndWithoutAConnection(t *testing.T) {
	for _, tt := range []struct{ name, service, body string }{
		{"without a connection", "strongswan-starter", "07[IKE] sending DPD request"},
		{"with a connection", "strongswan-starter", "13[IKE] <vpn-client|82> CHILD_SA vpn-client{3} closed"},
		{"service named as a unit", "strongswan-starter.service", "07[IKE] sending DPD request"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, defaultConfig())

			lr, _ := h.run(charonInput(tt.service, tt.body))

			if _, ok := lr.Attributes().Get("ipsec.component"); !ok {
				t.Errorf("not claimed: attributes %v", lr.Attributes().AsRaw())
			}
			if got := h.counter(unmatchedMetric, "source"); len(got) != 0 {
				t.Errorf("unmatched = %v", got)
			}
		})
	}
}

func TestCharon_ADifferentServiceIsNotClaimed(t *testing.T) {
	for _, service := range []string{"sshd", "strongswan", "charon", "strongswan-starter-x", ""} {
		t.Run(service, func(t *testing.T) {
			cfg := defaultConfig()
			cfg.Branches = []string{"charon"}
			h := newHarness(t, cfg)
			const body = "07[IKE] sending DPD request"

			lr, _ := h.run(charonInput(service, body))

			w4Untouched(t, lr, body, map[string]any{})
		})
	}
}

func TestCharon_ALineThatIsNoCharonLineIsNotClaimed(t *testing.T) {
	cfg := defaultConfig()
	cfg.Branches = []string{"charon"}
	h := newHarness(t, cfg)
	const body = "Starting strongSwan 5.9.8 IPsec [starter]..."

	lr, _ := h.run(charonInput("strongswan-starter", body))

	w4Untouched(t, lr, body, map[string]any{})
	if got := h.counter(parseFailedMetric, "format"); len(got) != 0 {
		t.Errorf("parse_failed = %v", got)
	}
}

func TestCharon_LineWithoutAConnection_GivesTheBodyTheThreadAndTheComponent(t *testing.T) {
	h := newHarness(t, defaultConfig())

	lr, res := h.run(charonInput("strongswan-starter", "07[IKE] sending DPD request"))

	w4Body(t, lr, "[IKE] sending DPD request")
	a := lr.Attributes()
	attrInt(t, a, "thread.id", 7)
	if attrStr(t, a, "ipsec.component") != "IKE" {
		t.Errorf("attributes = %v", a.AsRaw())
	}
	if attrStr(t, a, "log.record.original") != "07[IKE] sending DPD request" {
		t.Errorf("log.record.original = %v", a.AsRaw())
	}
	absent(t, a, "ipsec.connection_id", "ipsec.connection_seq", "ipsec.thread_id")
	if attrStr(t, res.Attributes(), "service.name") != "strongswan-starter" {
		t.Errorf("resource = %v", res.Attributes().AsRaw())
	}
}

func TestCharon_LineWithAConnection_KeepsTheIPsecAttributes(t *testing.T) {
	h := newHarness(t, defaultConfig())

	lr, _ := h.run(charonInput("strongswan-starter", "13[IKE] <vpn-client|82> CHILD_SA vpn-client{3} closed"))

	w4Body(t, lr, "[IKE] CHILD_SA vpn-client{3} closed")
	a := lr.Attributes()
	attrInt(t, a, "thread.id", 13)
	for k, want := range map[string]string{
		"ipsec.component": "IKE", "ipsec.connection_id": "vpn-client", "ipsec.connection_seq": "82",
		"ipsec.child_sa": "vpn-client", "ipsec.child_sa_num": "3", "ipsec.child_sa_state": "closed",
	} {
		if got := attrStr(t, a, k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
}

func TestCharon_ThreadNumber_ZeroIsZeroAndTextIsUnset(t *testing.T) {
	h := newHarness(t, defaultConfig())

	zero, _ := h.run(charonInput("strongswan-starter", "00[DMN] Starting IKE charon daemon (strongSwan 5.9.8)"))
	text, _ := h.run(charonInput("strongswan-starter", "ab[IKE] sending DPD request"))

	attrInt(t, zero.Attributes(), "thread.id", 0)
	absent(t, text.Attributes(), "thread.id")
	w4Body(t, text, "[IKE] sending DPD request")
}

func TestCharon_PacketLine_ShortensTheBodyAndKeepsTheAddresses(t *testing.T) {
	h := newHarness(t, defaultConfig())

	lr, _ := h.run(charonInput("strongswan-starter", "14[NET] sending packet: from 192.0.2.1[500] to 203.0.113.50[500] (1164 bytes)"))

	w4Body(t, lr, "[NET] sending packet 1164 bytes 203.0.113.50")
	for k, want := range map[string]string{"ipsec.direction": "sending", "ipsec.local_ip": "192.0.2.1", "ipsec.remote_ip": "203.0.113.50", "ipsec.packet_size": "1164"} {
		if got := attrStr(t, lr.Attributes(), k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
}

func TestCharon_Severity_FailureLinesAreWarnOrErrorAndTheRestStayUnset(t *testing.T) {
	for _, tt := range []struct {
		name string
		body string
		want plog.SeverityNumber
	}{
		{"auth failed notify", "15[IKE] received AUTH_FAILED notify error", plog.SeverityNumberError},
		{"no shared key", "15[IKE] <vpn-client|2> no shared key found for 'a.example.org' - 'b.example.org'", plog.SeverityNumberError},
		{"giving up", "15[IKE] <vpn-client|2> giving up after 5 retransmits", plog.SeverityNumberError},
		{"proposal refused", "15[IKE] peer didn't accept the proposal", plog.SeverityNumberWarn},
		{"retransmit", "15[IKE] <vpn-client|2> retransmit 3 of request with message ID 0", plog.SeverityNumberWarn},
		{"dpd", "07[IKE] sending DPD request", plog.SeverityNumberUnspecified},
		{"established", "16[IKE] IKE_SA vpn-client[3] established between 192.0.2.1[gw.example.org]...203.0.113.50[client.example.org]", plog.SeverityNumberUnspecified},
		{"unknown line", "16[JOB] something the table does not know", plog.SeverityNumberUnspecified},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, defaultConfig())

			lr, _ := h.run(charonInput("strongswan-starter", tt.body))

			if lr.SeverityNumber() != tt.want {
				t.Errorf("severity = %d %q, want %d", lr.SeverityNumber(), lr.SeverityText(), tt.want)
			}
			if lr.SeverityNumber() == plog.SeverityNumberInfo {
				t.Errorf("the table gave INFO")
			}
		})
	}
}

func TestCharon_Severity_AFailureLineBeatsTheLevelAnEarlierStageSet(t *testing.T) {
	h := newHarness(t, defaultConfig())
	in := charonInput("strongswan-starter", "15[IKE] received AUTH_FAILED notify error")
	in.attrs = map[string]any{"PRIORITY": "6"}

	ld := in.logs()
	ld.ResourceLogs().At(0).ScopeLogs().At(0).LogRecords().At(0).SetSeverityNumber(plog.SeverityNumberInfo)
	if err := h.proc.ConsumeLogs(t.Context(), ld); err != nil {
		t.Fatal(err)
	}

	all := h.sink.AllLogs()
	lr := all[len(all)-1].ResourceLogs().At(0).ScopeLogs().At(0).LogRecords().At(0)
	if lr.SeverityNumber() != plog.SeverityNumberError {
		t.Errorf("severity = %d, want ERROR", lr.SeverityNumber())
	}
}

func TestCharon_Severity_AnOrdinaryLineKeepsTheLevelAnEarlierStageSet(t *testing.T) {
	h := newHarness(t, defaultConfig())
	ld := charonInput("strongswan-starter", "07[IKE] sending DPD request").logs()
	ld.ResourceLogs().At(0).ScopeLogs().At(0).LogRecords().At(0).SetSeverityNumber(plog.SeverityNumberInfo)

	if err := h.proc.ConsumeLogs(t.Context(), ld); err != nil {
		t.Fatal(err)
	}

	all := h.sink.AllLogs()
	lr := all[len(all)-1].ResourceLogs().At(0).ScopeLogs().At(0).LogRecords().At(0)
	if lr.SeverityNumber() != plog.SeverityNumberInfo {
		t.Errorf("severity = %d, want the INFO the journal gave", lr.SeverityNumber())
	}
}

func TestCharon_ARecordThatHasBeenShapedIsShapedOnce(t *testing.T) {
	h := newHarness(t, defaultConfig())
	first, _ := h.run(charonInput("strongswan-starter", "07[IKE] sending DPD request"))

	second, _ := newHarness(t, defaultConfig()).run(charonInput("strongswan-starter", first.Body().Str()))

	w4Body(t, second, "[IKE] sending DPD request")
}
