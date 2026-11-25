// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package winlogprocessor

import (
	"strings"
	"testing"

	"go.opentelemetry.io/collector/pdata/plog"
)

const realNetlogonRecord = "_ldap._tcp.ForestDnsZones.example.org. 600 IN SRV 0 100 389 dc01.example.org."

// realNetlogon5774 has the shape of a captured NETLOGON 5774 event: the
// message text and the positional parameters are the real ones (param2 is the
// message id placeholder, param3 the DNS server address, param4 the RCODE,
// param5 the status code). Host and domain names are replaced with neutral ones.
func realNetlogon5774() receiverEvent {
	return receiverEvent{
		channel:  "System",
		provider: "NETLOGON",
		computer: "dc01.example.org",
		id:       5774,
		recordID: 31877,
		level:    "Error",
		task:     "0",
		opcode:   "Info",
		keywords: []any{"Classic"},
		message: "The dynamic registration of the DNS record '" + realNetlogonRecord + "' failed on the following DNS server:  \n\n" +
			"DNS server IP address: :: \nReturned Response Code (RCODE): 5 \nReturned Status Code: 9017  \n\n" +
			"For computers and users to locate this domain controller, this record must be registered in DNS.  \n\n" +
			"USER ACTION  \nDetermine what might have caused this failure, resolve the problem, and initiate registration of the DNS records by the domain controller.  \n\n" +
			"ADDITIONAL DATA \nError Value: DNS bad key.",
		eventData: map[string]any{
			"binary": "0500",
			"param1": realNetlogonRecord,
			"param2": "%%9017",
			"param3": "::",
			"param4": "5",
			"param5": "9017",
		},
	}
}

func TestRealNetlogon5774SummaryUsesTheRealParameterPositions(t *testing.T) {
	event := realNetlogon5774()
	record := shapeCfg(t, &Config{}, event)
	want := "DNS registration failed: _ldap._tcp.ForestDnsZones.example.org. SRV (RCODE 5, status 9017: DNS bad key)"
	if got := record.Body().Str(); got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
	if got := attrString(t, record, "vstan.winlog.message"); got != event.message {
		t.Errorf("vstan.winlog.message = %q, want the rendered message", got)
	}
	if record.SeverityNumber() != plog.SeverityNumberError {
		t.Errorf("severity = %v, want ERROR", record.SeverityNumber())
	}
	if got := attrString(t, record, "winlog.event_data.param3"); got != "::" {
		t.Errorf("the DNS server address must stay in event data, got %q", got)
	}
}

func TestRealNetlogon5774NeverPrintsTheMessageIDPlaceholder(t *testing.T) {
	body := shapeCfg(t, &Config{}, realNetlogon5774()).Body().Str()
	if strings.Contains(body, "%%9017") || strings.Contains(body, "param2") {
		t.Errorf("body = %q, must not carry param2", body)
	}
}

func TestRealNetlogon5774WithoutAnErrorValueLineShowsTheNumbersOnly(t *testing.T) {
	event := realNetlogon5774()
	event.message = "The dynamic registration of the DNS record failed."
	want := "DNS registration failed: _ldap._tcp.ForestDnsZones.example.org. SRV (RCODE 5, status 9017)"
	if got := shapeCfg(t, &Config{}, event).Body().Str(); got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
}

func TestRealNetlogon5774ARecordWithoutINIsUsedWhole(t *testing.T) {
	event := realNetlogon5774()
	event.eventData["param1"] = "_ldap._tcp.example.org."
	want := "DNS registration failed: _ldap._tcp.example.org. (RCODE 5, status 9017: DNS bad key)"
	if got := shapeCfg(t, &Config{}, event).Body().Str(); got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
}

func TestRealNetlogon5774WithTooFewParametersKeepsTheBody(t *testing.T) {
	event := realNetlogon5774()
	event.eventData = map[string]any{"param1": realNetlogonRecord, "param2": "%%9017", "param3": "::"}
	record := shapeCfg(t, &Config{}, event)
	if got := record.Body().Str(); got != event.message {
		t.Errorf("body = %q, want the message kept", got)
	}
	if _, ok := record.Attributes().Get("vstan.winlog.message"); ok {
		t.Error("vstan.winlog.message is set although no summary was made")
	}
	if record.SeverityNumber() != plog.SeverityNumberError {
		t.Errorf("severity = %v, want ERROR even without a summary", record.SeverityNumber())
	}
}

func TestNetlogon5774IgnoresTheOldGuessedFieldNames(t *testing.T) {
	event := realNetlogon5774()
	event.eventData = map[string]any{"DnsRecord": realNetlogonRecord, "Rcode": "5", "Status": "9017", "StatusText": "DNS bad key"}
	if got := shapeCfg(t, &Config{}, event).Body().Str(); got != event.message {
		t.Errorf("a row without the positional parameters must keep its body, got %q", got)
	}
}
