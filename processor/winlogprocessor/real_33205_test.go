// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package winlogprocessor

import (
	"strings"
	"testing"

	"go.opentelemetry.io/collector/pdata/plog"
)

// realStatementToken is a distinctive word of the audited SQL text: the
// statement can hold anything, so it must never reach the summary line.
const realStatementToken = "SECRET_TABLE_TOKEN"

// realAudit33205Text has the shape of the text of a captured MSSQL audit event
// 33205 (a login): one key:value pair for each line, a multi-line statement and
// an XML additional_information, with the application_name after them. Names,
// addresses and ids are neutral.
func realAudit33205Text() string {
	return strings.Join([]string{
		"Audit event: audit_schema_version:1",
		"event_time:2026-11-20 13:08:33.1455703",
		"sequence_number:1",
		"action_id:LGIS",
		"succeeded:true",
		"is_column_permission:false",
		"session_id:64",
		"server_principal_id:266",
		"database_principal_id:0",
		"object_id:0",
		"class_type:LX",
		"duration_milliseconds:0",
		"response_rows:0",
		"affected_rows:0",
		"client_tls_version:771",
		"client_ip:192.0.2.10",
		"permission_bitmask:00000000000000000000000000000000",
		"sequence_group_id:00000000-0000-4000-8000-000000000001",
		"session_server_principal_name:monitor_login",
		"server_principal_name:monitor_login",
		"server_principal_sid:S-1-0-0",
		"database_principal_name:",
		"server_instance_name:SQLHOST01\\SQLEXPRESS",
		"database_name:",
		"schema_name:",
		"object_name:",
		"statement:-- network protocol: LPC",
		"set quoted_identifier on",
		"set arithabort off",
		"select * from " + realStatementToken,
		"additional_information:<action_info xmlns=\"http://schemas.microsoft.com/sqlserver/2008/sqlaudit_data\"><pooled_connection>1</pooled_connection></action_info>",
		"user_defined_information:",
		"application_name:SQLServerCEIP",
		"connection_id:00000000-0000-4000-8000-000000000002",
		"data_sensitivity_information:",
		"host_name:SQLHOST01",
		"session_context:",
		"client_tls_version_name:1.2",
		"",
	}, "\n")
}

// realMSSQL33205 is the receiver record of that event: the rendered message and
// the single param1 carry the same text.
func realMSSQL33205() receiverEvent {
	text := realAudit33205Text()
	return receiverEvent{
		channel:   "Application",
		provider:  "MSSQL$SQLEXPRESS",
		computer:  "sql01.example.org",
		id:        33205,
		recordID:  90412,
		level:     "Information",
		task:      "Logon",
		opcode:    "0",
		keywords:  []any{"Audit Success", "Classic"},
		message:   text,
		eventData: map[string]any{"param1": text},
	}
}

func TestRealMSSQL33205SummaryLiftsTheAuditFields(t *testing.T) {
	event := realMSSQL33205()
	record := shapeCfg(t, &Config{}, event)
	want := "Login succeeded (LGIS) monitor_login app=SQLServerCEIP client=192.0.2.10"
	if got := record.Body().Str(); got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
	if got := attrString(t, record, "vstan.winlog.message"); got != event.message {
		t.Errorf("vstan.winlog.message must keep the rendered message")
	}
	if got := attrString(t, record, "winlog.event_data.param1"); got != event.message {
		t.Errorf("param1 must stay in event data")
	}
	if record.SeverityNumber() != plog.SeverityNumberInfo {
		t.Errorf("severity = %v, want INFO", record.SeverityNumber())
	}
}

func TestRealMSSQL33205ALocalConnectionShowsLocalMachine(t *testing.T) {
	event := realMSSQL33205()
	text := strings.Replace(realAudit33205Text(), "client_ip:192.0.2.10", "client_ip:local machine", 1)
	event.message, event.eventData = text, map[string]any{"param1": text}
	want := "Login succeeded (LGIS) monitor_login app=SQLServerCEIP client=local machine"
	if got := shapeCfg(t, &Config{}, event).Body().Str(); got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
}

func TestRealMSSQL33205ALoopbackClientIsShownAsWritten(t *testing.T) {
	event := realMSSQL33205()
	text := strings.Replace(realAudit33205Text(), "client_ip:192.0.2.10", "client_ip:127.0.0.1", 1)
	event.message, event.eventData = text, map[string]any{"param1": text}
	want := "Login succeeded (LGIS) monitor_login app=SQLServerCEIP client=127.0.0.1"
	if got := shapeCfg(t, &Config{}, event).Body().Str(); got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
}

func TestRealMSSQL33205AnAddressInAdditionalInformationIsNeverRead(t *testing.T) {
	text := strings.Replace(realAudit33205Text(), "client_ip:192.0.2.10", "client_ip:local machine", 1)
	text = strings.Replace(text, "<pooled_connection>1</pooled_connection>",
		"<address>203.0.113.77</address><pooled_connection>1</pooled_connection>", 1)
	event := realMSSQL33205()
	event.message, event.eventData = text, map[string]any{"param1": text}
	got := shapeCfg(t, &Config{}, event).Body().Str()
	if want := "Login succeeded (LGIS) monitor_login app=SQLServerCEIP client=local machine"; got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
	if strings.Contains(got, "203.0.113.77") {
		t.Errorf("body = %q carries the address of additional_information", got)
	}
}

func TestRealMSSQL33205NeverLiftsTheStatementOrAdditionalInformation(t *testing.T) {
	body := shapeCfg(t, &Config{}, realMSSQL33205()).Body().Str()
	for _, leak := range []string{realStatementToken, "quoted_identifier", "pooled_connection", "action_info", "LPC"} {
		if strings.Contains(body, leak) {
			t.Errorf("body = %q, must not carry %q", body, leak)
		}
	}
}

func TestRealMSSQL33205LinesInsideTheStatementCannotForgeTheSummary(t *testing.T) {
	forged := strings.Replace(realAudit33205Text(), "set arithabort off",
		strings.Join([]string{
			"action_id:DROP", "succeeded:false", "client_ip:203.0.113.99",
			"server_principal_name:evil_user", "application_name:evil_app",
		}, "\n"), 1)
	event := realMSSQL33205()
	event.message, event.eventData = forged, map[string]any{"param1": forged}
	want := "Login succeeded (LGIS) monitor_login app=SQLServerCEIP client=192.0.2.10"
	if got := shapeCfg(t, &Config{}, event).Body().Str(); got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
}

func TestRealMSSQL33205ConfigOverridesAnActionText(t *testing.T) {
	cfg := &Config{MSSQLActions: map[string]string{"LGIS": "Sign-in ok"}}
	want := "Sign-in ok (LGIS) monitor_login app=SQLServerCEIP client=192.0.2.10"
	if got := shapeCfg(t, cfg, realMSSQL33205()).Body().Str(); got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
}

func TestRealMSSQL33205AnUnknownActionShowsTheCodeAlone(t *testing.T) {
	text := strings.Replace(realAudit33205Text(), "action_id:LGIS", "action_id:ZZZZ", 1)
	event := realMSSQL33205()
	event.message, event.eventData = text, map[string]any{"param1": text}
	want := "ZZZZ monitor_login app=SQLServerCEIP client=192.0.2.10"
	if got := shapeCfg(t, &Config{}, event).Body().Str(); got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
}

func TestRealMSSQL33205AFailedActionSaysSo(t *testing.T) {
	cases := map[string]string{
		"SL":   "Select (SL) failed monitor_login app=SQLServerCEIP client=192.0.2.10",
		"LGIF": "Login failed (LGIF) monitor_login app=SQLServerCEIP client=192.0.2.10",
		"ZZZZ": "ZZZZ failed monitor_login app=SQLServerCEIP client=192.0.2.10",
	}
	for code, want := range cases {
		text := strings.Replace(realAudit33205Text(), "action_id:LGIS", "action_id:"+code, 1)
		text = strings.Replace(text, "succeeded:true", "succeeded:false", 1)
		event := realMSSQL33205()
		event.message, event.eventData = text, map[string]any{"param1": text}
		if got := shapeCfg(t, &Config{}, event).Body().Str(); got != want {
			t.Errorf("%s: body = %q, want %q", code, got, want)
		}
	}
}

func TestRealMSSQL33205FallsBackToTheMessageWithoutParam1(t *testing.T) {
	event := realMSSQL33205()
	event.eventData = nil
	want := "Login succeeded (LGIS) monitor_login app=SQLServerCEIP client=192.0.2.10"
	if got := shapeCfg(t, &Config{}, event).Body().Str(); got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
}

func TestMSSQL33205IgnoresTheOldGuessedFieldNames(t *testing.T) {
	event := realMSSQL33205()
	event.message = "Audit event"
	event.eventData = map[string]any{
		"action_id": "LGIS", "server_principal_name": "sa", "application_name": "App", "client_ip": "192.0.2.7",
	}
	if got := shapeCfg(t, &Config{}, event).Body().Str(); got != "Audit event" {
		t.Errorf("a row without param1 must keep its body, got %q", got)
	}
}
