// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package winlogprocessor

import (
	"strings"
	"testing"

	"go.opentelemetry.io/collector/pdata/plog"
)

const (
	realScriptBlockID = "80bda961-0264-4d8f-9288-627ce9adea5c"
	// realScriptToken is a distinctive word of the script text: it must never
	// reach the summary line.
	realScriptToken = "Export-ModuleMember"
)

// realPowerShell4104 has the shape of a captured PowerShell 4104 event. One
// script arrives as MessageTotal records that share ScriptBlockId; this is the
// last chunk (30 of 30). The script text is a made-up module export block, and
// the host names are neutral.
func realPowerShell4104() receiverEvent {
	chunk := "       }\r\n    }\r\n\r\n    # .EXTERNALHELP Example.cdxml-Help.xml\r\n}\r\n" +
		"Example.Module\\" + realScriptToken + " -Function 'Get-Example' -Alias '*'\r\n        \r\n\r\n"
	return receiverEvent{
		channel:   "Microsoft-Windows-PowerShell/Operational",
		provider:  "Microsoft-Windows-PowerShell",
		guid:      "{a0c1853b-5c40-4b15-8766-3cf1c58f985a}",
		computer:  "pc01.example.org",
		id:        4104,
		recordID:  48213,
		level:     "Warning",
		task:      "Execute a Remote Command",
		opcode:    "On create calls",
		keywords:  []any{"0x0"},
		userID:    "S-1-5-18",
		processID: 33200,
		threadID:  20016,
		message: "Creating Scriptblock text (30 of 30):\r\n" + chunk +
			"ScriptBlock ID: " + realScriptBlockID + "\r\nPath: ",
		eventData: map[string]any{
			"MessageNumber":   "30",
			"MessageTotal":    "30",
			"Path":            "",
			"ScriptBlockId":   realScriptBlockID,
			"ScriptBlockText": chunk,
		},
	}
}

func TestRealPowerShell4104SummaryNamesTheChunkAndTheScriptBlock(t *testing.T) {
	event := realPowerShell4104()
	record := shapeCfg(t, &Config{}, event)
	want := "PowerShell script block 80bda961 chunk 30 of 30"
	if got := record.Body().Str(); got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
	if record.SeverityNumber() != plog.SeverityNumberWarn {
		t.Errorf("severity = %v, want WARN", record.SeverityNumber())
	}
	if got := attrString(t, record, "winlog.event_data.ScriptBlockId"); got != realScriptBlockID {
		t.Errorf("ScriptBlockId = %q, must stay in event data", got)
	}
}

// The script text is large: it lives only in ScriptBlockText, not a second time
// in vstan.winlog.message.
func TestRealPowerShell4104KeepsTheScriptTextOnlyInScriptBlockText(t *testing.T) {
	event := realPowerShell4104()
	record := shapeCfg(t, &Config{}, event)
	if _, ok := record.Attributes().Get("vstan.winlog.message"); ok {
		t.Error("vstan.winlog.message is set for 4104: the script text would be stored twice")
	}
	if got, want := attrString(t, record, "winlog.event_data.ScriptBlockText"), event.eventData["ScriptBlockText"]; got != want {
		t.Errorf("ScriptBlockText = %q, want the chunk text %q", got, want)
	}
	if got := record.Body().Str(); strings.Contains(got, realScriptToken) {
		t.Errorf("body = %q must not carry the script text", got)
	}
}

func TestRealPowerShell4104WithoutASummaryKeepsTheBodyAndSetsNoMessageCopy(t *testing.T) {
	event := realPowerShell4104()
	event.eventData = map[string]any{"ScriptBlockText": "Get-Example"}
	record := shapeCfg(t, &Config{}, event)
	if got := record.Body().Str(); got != event.message {
		t.Errorf("body = %q, want the message kept", got)
	}
	if _, ok := record.Attributes().Get("vstan.winlog.message"); ok {
		t.Error("vstan.winlog.message is set although no summary was made")
	}
}

// Only 4104 skips the copy: every other summarized event keeps the full message.
func TestTheOtherSixEventsStillKeepTheFullMessage(t *testing.T) {
	for _, tc := range summaryCases() {
		if tc.event.id == 4104 {
			continue
		}
		record := shapeCfg(t, &Config{DNContractions: []string{dnSuffix}}, tc.event)
		if got := attrString(t, record, "vstan.winlog.message"); got != tc.event.message {
			t.Errorf("%s: vstan.winlog.message = %q, want the rendered message", tc.name, got)
		}
	}
}

func TestRealPowerShell4104NeverPutsTheScriptTextInTheBody(t *testing.T) {
	scripts := []string{
		realPowerShell4104().eventData["ScriptBlockText"].(string),
		realScriptToken + " -Function 'Get-Example' -Alias '*'", // the token opens the script
	}
	for _, script := range scripts {
		for _, chunk := range [][2]string{{"30", "30"}, {"1", "1"}, {"", ""}} {
			event := realPowerShell4104()
			event.eventData["ScriptBlockText"] = script
			event.eventData["MessageNumber"], event.eventData["MessageTotal"] = chunk[0], chunk[1]
			body := shapeCfg(t, &Config{}, event).Body().Str()
			if strings.Contains(body, realScriptToken) || strings.Contains(body, "Get-Example") || strings.ContainsAny(body, "\r\n") {
				t.Errorf("chunk %v: body = %q must not carry the script text or a line break", chunk, body)
			}
		}
	}
}

func TestRealPowerShell4104WithoutChunkNumbersNamesTheScriptBlockOnly(t *testing.T) {
	event := realPowerShell4104()
	delete(event.eventData, "MessageNumber")
	delete(event.eventData, "MessageTotal")
	want := "PowerShell script block 80bda961"
	if got := shapeCfg(t, &Config{}, event).Body().Str(); got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
}

func TestRealPowerShell4104WithoutAScriptBlockIDNamesTheChunkOnly(t *testing.T) {
	event := realPowerShell4104()
	delete(event.eventData, "ScriptBlockId")
	want := "PowerShell script block chunk 30 of 30"
	if got := shapeCfg(t, &Config{}, event).Body().Str(); got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
}

func TestRealPowerShell4104WithNoIdentifyingFieldsKeepsTheBodyAndTheSeverity(t *testing.T) {
	event := realPowerShell4104()
	event.eventData = map[string]any{"ScriptBlockText": "Get-Example"}
	record := shapeCfg(t, &Config{}, event)
	if got := record.Body().Str(); got != event.message {
		t.Errorf("body = %q, want the message kept", got)
	}
	if record.SeverityNumber() != plog.SeverityNumberWarn {
		t.Errorf("severity = %v, want WARN even without a summary", record.SeverityNumber())
	}
}

func TestRealPowerShell4104ANonEmptyPathIsAppended(t *testing.T) {
	event := realPowerShell4104()
	event.eventData["Path"] = `C:\Scripts\example.ps1`
	want := `PowerShell script block 80bda961 chunk 30 of 30 (C:\Scripts\example.ps1)`
	if got := shapeCfg(t, &Config{}, event).Body().Str(); got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
}

func TestRealPowerShell4104KeepsTheRenderedOpcodeAndTask(t *testing.T) {
	record := shapeCfg(t, &Config{}, realPowerShell4104())
	if got := attrString(t, record, "winlog.opcode"); got != "On create calls" {
		t.Errorf("winlog.opcode = %q, want On create calls", got)
	}
	if got := attrString(t, record, "winlog.task"); got != "Execute a Remote Command" {
		t.Errorf("winlog.task = %q, want Execute a Remote Command", got)
	}
}
