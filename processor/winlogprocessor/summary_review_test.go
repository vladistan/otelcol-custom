// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package winlogprocessor

import (
	"strings"
	"testing"
	"unicode/utf8"

	"go.opentelemetry.io/collector/pdata/plog"
)

func powerShellEvent(task, opcode string) receiverEvent {
	return receiverEvent{
		channel: "Microsoft-Windows-PowerShell/Operational", provider: "Microsoft-Windows-PowerShell", id: 4104,
		task: task, opcode: opcode, message: "Creating Scriptblock text (1 of 1)",
	}
}

func TestPowerShell4104RenderedWrongTextIsReplacedFromTheProviderTable(t *testing.T) {
	record := shapeCfg(t, &Config{}, powerShellEvent("Some other task", "To be used when an exception is raised"))
	if got := attrString(t, record, "winlog.opcode"); got != "On create calls" {
		t.Errorf("winlog.opcode = %q, want On create calls", got)
	}
	if got := attrString(t, record, "winlog.task"); got != "Execute a Remote Command" {
		t.Errorf("winlog.task = %q, want Execute a Remote Command", got)
	}
}

func TestPowerShell4104WithNoTaskOrOpcodeGetsTheProviderText(t *testing.T) {
	record := shapeCfg(t, &Config{}, powerShellEvent("", ""))
	if got := attrString(t, record, "winlog.opcode"); got != "On create calls" {
		t.Errorf("winlog.opcode = %q, want On create calls", got)
	}
}

func TestAnotherEventOfTheProviderKeepsItsRenderedText(t *testing.T) {
	event := powerShellEvent("Rendered task", "Rendered opcode")
	event.id = 4103
	record := shapeCfg(t, &Config{}, event)
	if attrString(t, record, "winlog.task") != "Rendered task" || attrString(t, record, "winlog.opcode") != "Rendered opcode" {
		t.Errorf("text changed for an event with no table entry: %v", record.Attributes().AsRaw())
	}
}

func TestOpcode240IsReceive(t *testing.T) {
	record := shapeCfg(t, &Config{}, receiverEvent{channel: "System", provider: "p", id: 1, opcode: "240"})
	if got := attrString(t, record, "winlog.opcode"); got != "Receive" {
		t.Errorf("winlog.opcode = %q, want Receive", got)
	}
}

func TestUnescapeControlsKeepsPathsAndConvertsRealSequences(t *testing.T) {
	cases := map[string]string{
		`line one\r\nline two\n\tindented`:   "line one\r\nline two\n\tindented",
		`\\nas01\temp`:                       `\\nas01\temp`,
		`via DC \\nas01.lab.example\new`:     `via DC \\nas01.lab.example\new`,
		`copy C:\new\tab to D:\temp\now`:     `copy C:\new\tab to D:\temp\now`,
		`first\nthen \\nas01\temp\tools ok`:  "first\nthen " + `\\nas01\temp\tools` + " ok",
		`"\\server\tools" and "C:\temp"\n.`:  `"\\server\tools" and "C:\temp"` + "\n.",
		`no backslashes here`:                `no backslashes here`,
		`trailing backslash \`:               `trailing backslash \`,
		`DOMAIN\user then\nnext`:             "DOMAIN\\user then\nnext",
		`a=\\nas01\temp,b=C:\new,c=\tvalue`:  `a=\\nas01\temp,b=C:\new,c=` + "\tvalue",
		`\\nas01\temp\r\nnext line`:          `\\nas01\temp\r\nnext line`,
		`C:\new\tab` + `\n` + `after`:        `C:\new\tab\nafter`,
		`end\r\n\\server\share\nope and \nx`: "end\r\n" + `\\server\share\nope` + " and \nx",
	}
	for in, want := range cases {
		if got := unescapeControls(in); got != want {
			t.Errorf("unescapeControls(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPathsInEventDataAndMessageAreNeverRewritten(t *testing.T) {
	const unc = `\\nas01\temp`
	event := receiverEvent{
		channel: "System", provider: "Microsoft-Windows-GroupPolicy", id: 5310,
		message:   `Account details via DC ` + unc + `\new`,
		eventData: map[string]any{"PrincipalCNName": "CN=DC1" + dnSuffix, "DCName": unc},
	}
	record := shapeCfg(t, &Config{DNContractions: []string{dnSuffix}}, event)
	if got, want := record.Body().Str(), "Account details: CN=DC1,... via DC "+unc; got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
	if got := attrString(t, record, "vstan.winlog.message"); got != event.message {
		t.Errorf("vstan.winlog.message = %q, want the message unchanged %q", got, event.message)
	}
	if strings.ContainsAny(record.Body().Str(), "\n\t") {
		t.Errorf("body holds a control character: %q", record.Body().Str())
	}
}

func TestAScriptBlockWithAUNCPathKeepsItsBackslashesInTheFullText(t *testing.T) {
	const script = `Copy-Item \\nas01\temp\new.txt C:\temp\tab.txt`
	event := powerShellEvent("", "")
	event.message = "Creating Scriptblock text (1 of 1):\r\n" + script + "\r\n\r\nScriptBlock ID: aaaabbbb-1"
	event.eventData = map[string]any{
		"MessageNumber": "1", "MessageTotal": "1", "ScriptBlockId": "aaaabbbb-1", "ScriptBlockText": script,
	}
	record := shapeCfg(t, &Config{}, event)
	if got, want := record.Body().Str(), "PowerShell script block aaaabbbb chunk 1 of 1"; got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
	if _, ok := record.Attributes().Get("vstan.winlog.message"); ok {
		t.Error("vstan.winlog.message is set for 4104: the script lives only in ScriptBlockText")
	}
	if got := attrString(t, record, "winlog.event_data.ScriptBlockText"); got != script {
		t.Errorf("ScriptBlockText = %q, want it unchanged", got)
	}
}

func TestSeverityIsSetByProviderAndIDWhenThereIsNoSummary(t *testing.T) {
	cases := []struct {
		name  string
		cfg   *Config
		event receiverEvent
		want  plog.SeverityNumber
	}{
		{"NETLOGON 5774 without fields", &Config{}, receiverEvent{channel: "System", provider: "NETLOGON", id: 5774, message: "m"}, plog.SeverityNumberError},
		{"PowerShell 4104 without fields", &Config{}, powerShellEvent("", ""), plog.SeverityNumberWarn},
		{
			"NETLOGON 5774 with the summary turned off", &Config{Summaries: map[string]string{"NETLOGON/5774": ""}},
			summaryCases()[1].event, plog.SeverityNumberError,
		},
		{
			"NETLOGON 5774 with a template that finds no field", &Config{Summaries: map[string]string{"NETLOGON/5774": "x {Nothing}"}},
			summaryCases()[1].event, plog.SeverityNumberError,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shapeCfg(t, tc.cfg, tc.event).SeverityNumber(); got != tc.want {
				t.Errorf("severity = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestATemplateThatFillsNoPlaceholderKeepsTheBody(t *testing.T) {
	cfg := &Config{Summaries: map[string]string{"NETLOGON/5774": "Failed {Missing} {AlsoMissing}"}}
	event := receiverEvent{channel: "System", provider: "NETLOGON", id: 5774, message: "The dynamic registration failed."}
	record := shapeCfg(t, cfg, event)
	if got := record.Body().Str(); got != event.message {
		t.Errorf("body = %q, want the message kept", got)
	}
	if _, ok := record.Attributes().Get("vstan.winlog.message"); ok {
		t.Error("vstan.winlog.message is set although no summary was made")
	}
}

func TestATemplateWithOnlyLiteralTextReplacesTheBody(t *testing.T) {
	cfg := &Config{Summaries: map[string]string{"NETLOGON/5774": "Registration failed"}}
	event := receiverEvent{channel: "System", provider: "NETLOGON", id: 5774, message: "The dynamic registration failed."}
	if got := shapeCfg(t, cfg, event).Body().Str(); got != "Registration failed" {
		t.Errorf("body = %q, want the literal template", got)
	}
}

func TestTwoSummaryKeysThatNormalizeAlikeAreRejected(t *testing.T) {
	cfg := &Config{Summaries: map[string]string{"NETLOGON/5774": "a", "netlogon/5774": "b"}}
	if err := cfg.Validate(); err == nil {
		t.Error("Validate accepted two keys for the same provider and event id")
	}
	cfg = &Config{Summaries: map[string]string{"Microsoft-Windows-Foo/42": "a", "foo/42": "b"}}
	if err := cfg.Validate(); err == nil {
		t.Error("Validate accepted a key with and without the provider prefix")
	}
}

func TestOneLineCutsLikeBeforeAndDoesNotCopyTheWholeScript(t *testing.T) {
	cases := []struct {
		in   string
		max  int
		want string
	}{
		{"  a   b\n\tc  ", 10, "a b c"},
		{"abc def", 7, "abc def"},
		{"abc def", 6, "abc de..."},
		{"abc def", 3, "abc..."},
		{"abc  ", 3, "abc"},
		{"abc \n x", 3, "abc..."},
		{"héllo wörld", 8, "héllo wö..."},
		{"", 5, ""},
		{" \t\n", 5, ""},
	}
	for _, tc := range cases {
		if got := oneLine(tc.in, tc.max); got != tc.want {
			t.Errorf("oneLine(%q, %d) = %q, want %q", tc.in, tc.max, got, tc.want)
		}
	}
	big := strings.Repeat("Write-Output 'x'\r\n", 64*1024)
	allocs := testing.AllocsPerRun(3, func() { _ = oneLine(big, 60) })
	if allocs > 4 {
		t.Errorf("oneLine of a %d byte script made %v allocations, want at most 4", len(big), allocs)
	}
}

func TestInvalidUTF8InEventDataIsMadeValidInTheSummaryAndTheFullText(t *testing.T) {
	event := receiverEvent{
		channel: "System", provider: "NETLOGON", id: 5774, message: "bad \xff\xfe byte",
		eventData: map[string]any{"param1": "host\xff.lab.example.org. 600 IN SRV 0 1 2 d.", "param4": "5"},
	}
	record := shapeCfg(t, &Config{}, event)
	body := record.Body().Str()
	if !utf8.ValidString(body) || !strings.Contains(body, "host\uFFFD.lab.example.org.") {
		t.Errorf("body = %q, want valid UTF-8 with one U+FFFD for the bad byte", body)
	}
	full := attrString(t, record, "vstan.winlog.message")
	if !utf8.ValidString(full) || full != "bad \uFFFD\uFFFD byte" {
		t.Errorf("vstan.winlog.message = %q, want each bad byte replaced by U+FFFD", full)
	}
}

func TestAFirewallEventOtherThan2011WithPlaceholdersAlsoGetsTheMarker(t *testing.T) {
	event := receiverEvent{
		channel:  "Microsoft-Windows-Windows Firewall With Advanced Security/Firewall",
		provider: "Microsoft-Windows-Windows Firewall With Advanced Security", id: 2004,
		message: "A rule has been added: %1 %2",
	}
	if v, ok := shapeCfg(t, &Config{}, event).Attributes().Get("vstan.winlog.unsubstituted"); !ok || !v.Bool() {
		t.Errorf("marker = %v, want true for any Windows Firewall event with unfilled placeholders", v)
	}
}
