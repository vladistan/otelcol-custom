// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package winlogprocessor

import (
	"context"
	"testing"

	"go.opentelemetry.io/collector/pdata/plog"
)

func shapeCfg(t *testing.T, cfg *Config, event receiverEvent) plog.LogRecord {
	t.Helper()
	h := newHarnessCfg(t, cfg)
	logs := plog.NewLogs()
	event.toLogRecord().CopyTo(logs.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty().LogRecords().AppendEmpty())
	if err := h.proc.ConsumeLogs(context.Background(), logs); err != nil {
		t.Fatalf("consume: %v", err)
	}
	if got := h.sink.LogRecordCount(); got != 1 {
		t.Fatalf("forwarded %d records, want 1", got)
	}
	return h.sink.AllLogs()[0].ResourceLogs().At(0).ScopeLogs().At(0).LogRecords().At(0)
}

const dnSuffix = ",OU=Domain Controllers,DC=lab,DC=example,DC=org"

type summaryCase struct {
	name     string
	event    receiverEvent
	want     string
	severity plog.SeverityNumber // plog.SeverityNumberUnspecified = not asserted
}

func summaryCases() []summaryCase {
	return []summaryCase{
		{
			name:  "GroupPolicy 5310",
			event: realGroupPolicy5310(),
			want:  `Account details: CN=DC01,OU=Domain Controllers,DC=example,DC=org via DC \\dc01.example.org`,
		},
		{
			name:     "NETLOGON 5774",
			event:    realNetlogon5774(),
			want:     "DNS registration failed: _ldap._tcp.ForestDnsZones.example.org. SRV (RCODE 5, status 9017: DNS bad key)",
			severity: plog.SeverityNumberError,
		},
		{
			name:     "PowerShell 4104",
			event:    realPowerShell4104(),
			want:     "PowerShell script block 80bda961 chunk 30 of 30",
			severity: plog.SeverityNumberWarn,
		},
		{
			name:     "MSSQL 33205",
			event:    realMSSQL33205(),
			want:     "Login succeeded (LGIS) monitor_login app=SQLServerCEIP client=192.0.2.10",
			severity: plog.SeverityNumberInfo,
		},
		{
			name:     "Defender 1151",
			event:    realDefender1151("4", "Enabled"),
			want:     defenderBase,
			severity: plog.SeverityNumberInfo,
		},
		{
			name:     "Kernel-Cache 105",
			event:    realKernelCache105([12]int{}),
			want:     "Cache write latency over 3650 s: no write IO",
			severity: plog.SeverityNumberInfo,
		},
		{
			name:     "StorPort 505",
			event:    realStorPort505([12]int{}),
			want:     "StorPort 2:0:0:0 QEMU HARDDISK: 5819 IO, read 108.0 KiB, written 84.3 MiB, max latency 1915.2 ms",
			severity: plog.SeverityNumberInfo,
		},
	}
}

func TestSevenEventsGetAOneLineSummary(t *testing.T) {
	for _, tc := range summaryCases() {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &Config{DNContractions: []string{dnSuffix}}
			record := shapeCfg(t, cfg, tc.event)
			if got := record.Body().Str(); got != tc.want {
				t.Errorf("body = %q, want %q", got, tc.want)
			}
			if tc.event.id == 4104 {
				// The script stays only in ScriptBlockText: no second copy.
				if _, ok := record.Attributes().Get("vstan.winlog.message"); ok {
					t.Error("vstan.winlog.message is set for 4104")
				}
			} else if got := attrString(t, record, "vstan.winlog.message"); got != tc.event.message {
				t.Errorf("vstan.winlog.message = %q, want the rendered message %q", got, tc.event.message)
			}
			if tc.severity != plog.SeverityNumberUnspecified && record.SeverityNumber() != tc.severity {
				t.Errorf("severity = %v, want %v", record.SeverityNumber(), tc.severity)
			}
		})
	}
}

func TestNETLOGONAndPowerShellSeverityIsSetWithoutXML(t *testing.T) {
	cases := summaryCases()
	for _, i := range []int{1, 2} {
		record := shapeCfg(t, &Config{}, cases[i].event)
		if record.SeverityNumber() != cases[i].severity {
			t.Errorf("%s severity = %v, want %v", cases[i].name, record.SeverityNumber(), cases[i].severity)
		}
	}
}

func TestAnEventWithoutATemplateKeepsItsBodyAndAttributes(t *testing.T) {
	event := receiverEvent{
		channel: "Application", provider: "OldProvider", id: 1000, message: "Plain message.",
		eventData: map[string]any{"a": "1"},
	}
	record := shapeCfg(t, &Config{}, event)
	if got := record.Body().Str(); got != "Plain message." {
		t.Errorf("body = %q, want the message unchanged", got)
	}
	if _, ok := record.Attributes().Get("vstan.winlog.message"); ok {
		t.Error("vstan.winlog.message is set for an event with no template")
	}
}

func TestTemplatedProviderWithAnotherEventIDIsUnchanged(t *testing.T) {
	event := receiverEvent{channel: "System", provider: "NETLOGON", id: 5719, message: "No logon server."}
	if got := shapeCfg(t, &Config{}, event).Body().Str(); got != "No logon server." {
		t.Errorf("body = %q, want the message unchanged", got)
	}
}

func TestASummaryWithoutItsFieldsKeepsTheMessage(t *testing.T) {
	event := receiverEvent{channel: "System", provider: "NETLOGON", id: 5774, message: "The dynamic registration failed."}
	record := shapeCfg(t, &Config{}, event)
	if got := record.Body().Str(); got != "The dynamic registration failed." {
		t.Errorf("body = %q, want the message when no field is present", got)
	}
}

// mssqlEvent is an audit event whose only parameter is the key:value text.
func mssqlEvent(data map[string]any) receiverEvent {
	return receiverEvent{channel: "Application", provider: "MSSQLSERVER", id: 33205, message: "Audit event", eventData: data}
}

func auditText(code, principal string) string {
	return "action_id:" + code + "\nsucceeded:true\nserver_principal_name:" + principal + "\n"
}

func TestMSSQLActionCodesComeFromTheDefaultTable(t *testing.T) {
	for code, want := range map[string]string{"LGIF": "Login failed (LGIF) sa", "LGO": "Logout (LGO) sa", "ZZZZ": "ZZZZ sa"} {
		record := shapeCfg(t, &Config{}, mssqlEvent(map[string]any{"param1": auditText(code, "sa")}))
		if got := record.Body().Str(); got != want {
			t.Errorf("%s: body = %q, want %q", code, got, want)
		}
	}
}

func TestMSSQLActionCodeConfigOverridesTheDefault(t *testing.T) {
	cfg := &Config{MSSQLActions: map[string]string{"LGIS": "Sign-in ok", "ZZZZ": "Custom action"}}
	for code, want := range map[string]string{"LGIS": "Sign-in ok (LGIS) sa", "ZZZZ": "Custom action (ZZZZ) sa"} {
		record := shapeCfg(t, cfg, mssqlEvent(map[string]any{"param1": auditText(code, "sa")}))
		if got := record.Body().Str(); got != want {
			t.Errorf("%s: body = %q, want %q", code, got, want)
		}
	}
}

func TestMSSQLReadsKeyValueLinesFromOneField(t *testing.T) {
	text := "event_time:2026-11-20 04:00:00\r\naction_id:LGIS\r\nsucceeded:true\r\n" +
		"server_principal_name:app_user\r\napplication_name:Report\r\nclient_ip:192.0.2.7\r\n"
	record := shapeCfg(t, &Config{}, mssqlEvent(map[string]any{"param1": text}))
	if got, want := record.Body().Str(), "Login succeeded (LGIS) app_user app=Report client=192.0.2.7"; got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
}

func TestTheFullTextAttributeCarriesRealControlCharacters(t *testing.T) {
	event := summaryCases()[1].event
	event.message = `line one\r\nline two\n\tindented`
	record := shapeCfg(t, &Config{}, event)
	want := "line one\r\nline two\n\tindented"
	if got := attrString(t, record, "vstan.winlog.message"); got != want {
		t.Errorf("vstan.winlog.message = %q, want real CR, LF and tab: %q", got, want)
	}
}

func TestConfigTemplateOverridesTheDefaultAndAddsNewEvents(t *testing.T) {
	cfg := &Config{Summaries: map[string]string{
		"NETLOGON/5774":                      "DNS failure {param5}",
		"microsoft-windows-foo/42":           "Foo {Name|dn}",
		"Microsoft-Windows-GroupPolicy/5310": "",
	}, DNContractions: []string{dnSuffix}}
	if got := shapeCfg(t, cfg, summaryCases()[1].event).Body().Str(); got != "DNS failure 9017" {
		t.Errorf("override body = %q, want %q", got, "DNS failure 9017")
	}
	added := receiverEvent{channel: "System", provider: "Microsoft-Windows-Foo", id: 42, message: "m",
		eventData: map[string]any{"Name": "CN=X" + dnSuffix}}
	if got := shapeCfg(t, cfg, added).Body().Str(); got != "Foo CN=X,..." {
		t.Errorf("added body = %q, want %q", got, "Foo CN=X,...")
	}
	disabled := summaryCases()[0].event
	if got := shapeCfg(t, cfg, disabled).Body().Str(); got != disabled.message {
		t.Errorf("disabled template body = %q, want the message %q", got, disabled.message)
	}
}

func TestConfigRejectsBadSummaryKeysAndTemplates(t *testing.T) {
	cases := map[string]*Config{
		"no slash":       {Summaries: map[string]string{"NETLOGON": "x"}},
		"bad id":         {Summaries: map[string]string{"NETLOGON/abc": "x"}},
		"empty provider": {Summaries: map[string]string{"/5": "x"}},
		"open brace":     {Summaries: map[string]string{"NETLOGON/5": "a {Field"}},
		"empty mssql":    {MSSQLActions: map[string]string{"": "x"}},
		"empty dn":       {DNContractions: []string{""}},
	}
	for name, cfg := range cases {
		if err := cfg.Validate(); err == nil {
			t.Errorf("%s: Validate accepted the config", name)
		}
	}
}

func TestWinlogComputerNameIsDropped(t *testing.T) {
	record := shapeEvent(t, 0, fullSecurityEvent)
	if _, ok := record.Attributes().Get("winlog.computer_name"); ok {
		t.Error("winlog.computer_name is set, want it dropped")
	}
}

func TestAFirewallRecordWithUnsubstitutedPlaceholdersGetsAMarker(t *testing.T) {
	event := receiverEvent{
		channel:  "Microsoft-Windows-Windows Firewall With Advanced Security/Firewall",
		provider: "Microsoft-Windows-Windows Firewall With Advanced Security", id: 2011,
		message: "A rule has been deleted: %1 %2 %3 %4 %5 %6 %7",
	}
	record := shapeCfg(t, &Config{}, event)
	v, ok := record.Attributes().Get("vstan.winlog.unsubstituted")
	if !ok || !v.Bool() {
		t.Errorf("vstan.winlog.unsubstituted = %v, want true", v)
	}
	if got := record.Body().Str(); got != event.message {
		t.Errorf("body = %q, want the message unchanged, no invented values", got)
	}
}

func TestAFirewallRecordWithEventDataGetsNoMarker(t *testing.T) {
	event := receiverEvent{
		channel: "Security", provider: "Microsoft-Windows-Windows Firewall With Advanced Security", id: 2011,
		message: "Rule %1", eventData: map[string]any{"RuleName": "x"},
	}
	if _, ok := shapeCfg(t, &Config{}, event).Attributes().Get("vstan.winlog.unsubstituted"); ok {
		t.Error("marker set although event_data is present")
	}
}

func TestNumericOpcodeUsesTheWindowsOpcodeTable(t *testing.T) {
	event := receiverEvent{channel: "System", provider: "p", id: 1, opcode: "1"}
	if got := attrString(t, shapeCfg(t, &Config{}, event), "winlog.opcode"); got != "Start" {
		t.Errorf("winlog.opcode = %q, want Start", got)
	}
}

func TestRenderedOpcodeAndTaskAreKept(t *testing.T) {
	record := shapeCfg(t, &Config{}, receiverEvent{channel: "System", provider: "p", id: 1, opcode: "Info", task: "Logon"})
	if attrString(t, record, "winlog.opcode") != "Info" || attrString(t, record, "winlog.task") != "Logon" {
		t.Errorf("rendered opcode and task changed: %v", record.Attributes().AsRaw())
	}
}

func TestPowerShell4104OpcodeAndTaskUseTheProviderTable(t *testing.T) {
	event := receiverEvent{
		channel: "Microsoft-Windows-PowerShell/Operational", provider: "Microsoft-Windows-PowerShell", id: 4104,
		task: "106", opcode: "15",
	}
	record := shapeCfg(t, &Config{}, event)
	if got := attrString(t, record, "winlog.task"); got != "Execute a Remote Command" {
		t.Errorf("winlog.task = %q, want Execute a Remote Command", got)
	}
	if got := attrString(t, record, "winlog.opcode"); got != "On create calls" {
		t.Errorf("winlog.opcode = %q, want On create calls", got)
	}
}
