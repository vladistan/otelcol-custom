// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"testing"

	"go.opentelemetry.io/collector/pdata/plog"
)

// Invented fixture: the shape of a ClickHouse server line, not a capture.
const clickhouseInfo = `2026.11.19 23:01:21.644520 [ 1980900 ] {} <Information> TCPHandler: Client has not sent any data.`

func TestClickHouse_InformationLine_GivesTheMessageScopeAndThread(t *testing.T) {
	lr, scope := runScopedW4(t, input{body: clickhouseInfo})

	w4Body(t, lr, "Client has not sent any data.")
	w4Severity(t, lr, plog.SeverityNumberInfo, "INFO")
	w4Scope(t, scope, "TCPHandler")
	a := lr.Attributes()
	attrInt(t, a, "thread.id", 1980900)
	if attrStr(t, a, "log.record.original") != clickhouseInfo {
		t.Fatalf("attributes = %v", a.AsRaw())
	}
	absent(t, a, "vstan.clickhouse.query_id")
}

func TestClickHouse_QueryIDInBraces_IsAnAttribute(t *testing.T) {
	lr, _ := runW4(t, input{body: `2026.11.19 23:01:21.644520 [ 17 ] {4b1c-77} <Error> executeQuery: Code: 60. DB::Exception: no table`})

	if attrStr(t, lr.Attributes(), "vstan.clickhouse.query_id") != "4b1c-77" {
		t.Fatalf("attributes = %v", lr.Attributes().AsRaw())
	}
	w4Severity(t, lr, plog.SeverityNumberError, "ERROR")
}

func TestClickHouse_Levels_MapOnTheClickHouseTable(t *testing.T) {
	want := map[string]plog.SeverityNumber{
		"Trace": plog.SeverityNumberTrace, "Debug": plog.SeverityNumberDebug, "Warning": plog.SeverityNumberWarn,
		"Critical": plog.SeverityNumberFatal, "Notice": plog.SeverityNumberInfo,
	}
	for level, num := range want {
		lr, _ := runW4(t, input{body: "2026.11.19 23:01:21.644520 [ 1 ] {} <" + level + "> Foo: text"})
		if lr.SeverityNumber() != num {
			t.Fatalf("<%s> -> %d, want %d", level, lr.SeverityNumber(), num)
		}
	}
}

func TestClickHouse_StackTraceSuffix_IsDropped(t *testing.T) {
	body := "2026.11.19 23:01:21.644520 [ 17 ] {} <Error> executeQuery: Code: 60. DB::Exception: no table\n\n" +
		"Stack trace (when copying this message, always include the lines below):\n\n0. DB::Exception::Exception() @ 0x1\n1. foo @ 0x2"

	lr, _ := runW4(t, input{body: body})

	w4Body(t, lr, "Code: 60. DB::Exception: no table")
}

func TestClickHouse_UnknownLevel_LeavesTheSeverityUnset(t *testing.T) {
	lr, _ := runW4(t, input{body: "2026.11.19 23:01:21.644520 [ 1 ] {} <Chatty> Foo: text"})

	w4Body(t, lr, "text")
	w4Severity(t, lr, plog.SeverityNumberUnspecified, "")
}
