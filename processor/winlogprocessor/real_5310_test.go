// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package winlogprocessor

import (
	"testing"

	"go.opentelemetry.io/collector/pdata/plog"
)

// realGroupPolicy5310 has the shape of a captured GroupPolicy 5310 event:
// the message text and the four event data names are the real ones. Host,
// domain and distinguished names are replaced with neutral ones.
func realGroupPolicy5310() receiverEvent {
	return receiverEvent{
		channel:  "Microsoft-Windows-GroupPolicy/Operational",
		provider: "Microsoft-Windows-GroupPolicy",
		guid:     "{aea1b4fa-97d1-45f2-a64c-4d69fffd92c9}",
		computer: "dc01.example.org",
		id:       5310,
		recordID: 20511,
		level:    "Information",
		task:     "0",
		opcode:   "Info",
		keywords: []any{"0x4000000000000000"},
		message: "Account details: \n\tAccount Name : CN=DC01,OU=Domain Controllers,DC=example,DC=org\n" +
			"\tAccount Domain Name : example.org\n\tDC Name : \\\\dc01.example.org\n\tDC Domain Name : example.org",
		userID:    "S-1-5-18",
		processID: 1688, threadID: 3236,
		eventData: map[string]any{
			"PrincipalCNName":     "CN=DC01,OU=Domain Controllers,DC=example,DC=org",
			"PrincipalDomainName": "example.org",
			"DCName":              `\\dc01.example.org`,
			"DCDomainName":        "example.org",
		},
	}
}

const realDNSuffix = ",OU=Domain Controllers,DC=example,DC=org"

func TestRealGroupPolicy5310SummaryUsesTheRealFieldNames(t *testing.T) {
	event := realGroupPolicy5310()
	record := shapeCfg(t, &Config{DNContractions: []string{realDNSuffix}}, event)
	if got, want := record.Body().Str(), `Account details: CN=DC01,... via DC \\dc01.example.org`; got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
	if got := attrString(t, record, "vstan.winlog.message"); got != event.message {
		t.Errorf("vstan.winlog.message = %q, want the rendered message %q", got, event.message)
	}
	if got := attrString(t, record, "winlog.event_data.PrincipalCNName"); got != "CN=DC01,OU=Domain Controllers,DC=example,DC=org" {
		t.Errorf("the full DN must stay in event data, got %q", got)
	}
	if record.SeverityNumber() != plog.SeverityNumberInfo {
		t.Errorf("severity = %v, want INFO", record.SeverityNumber())
	}
}

func TestRealGroupPolicy5310WithoutAContractionKeepsTheWholeDN(t *testing.T) {
	record := shapeCfg(t, &Config{}, realGroupPolicy5310())
	want := `Account details: CN=DC01,OU=Domain Controllers,DC=example,DC=org via DC \\dc01.example.org`
	if got := record.Body().Str(); got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
}

func TestRealGroupPolicy5310UnlistedSuffixStaysWhole(t *testing.T) {
	record := shapeCfg(t, &Config{DNContractions: []string{",OU=Elsewhere,DC=example,DC=org"}}, realGroupPolicy5310())
	want := `Account details: CN=DC01,OU=Domain Controllers,DC=example,DC=org via DC \\dc01.example.org`
	if got := record.Body().Str(); got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
}

func TestRealGroupPolicy5310WithoutTheDCNameStillSummarizes(t *testing.T) {
	event := realGroupPolicy5310()
	delete(event.eventData, "DCName")
	record := shapeCfg(t, &Config{DNContractions: []string{realDNSuffix}}, event)
	if got, want := record.Body().Str(), "Account details: CN=DC01,..."; got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
}

func TestGroupPolicy5310IgnoresTheOldGuessedFieldNames(t *testing.T) {
	event := realGroupPolicy5310()
	event.eventData = map[string]any{"AccountDN": "CN=DC01" + realDNSuffix, "DCName": `\\dc01.example.org`}
	record := shapeCfg(t, &Config{DNContractions: []string{realDNSuffix}}, event)
	if got := record.Body().Str(); got != event.message {
		t.Errorf("a row without PrincipalCNName must keep its body, got %q", got)
	}
}
