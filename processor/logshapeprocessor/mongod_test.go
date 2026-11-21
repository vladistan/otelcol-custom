// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

// cspell:ignore vstan

package logshapeprocessor

import (
	"testing"
	"time"

	"go.opentelemetry.io/collector/pdata/plog"
)

const (
	// Invented fixtures: shapes of mongod lines with a documentation address, not captures. First: a connection line.
	mongodEnded = `{"t":{"$date":"2026-11-19T22:45:00.123+00:00"},"s":"I","c":"NETWORK","id":22944,"ctx":"conn1131180","msg":"Connection ended","attr":{"remote":"192.0.2.10:53714","uuid":{"uuid":{"$uuid":"1a2b3c4d-0000-4000-8000-000000000001"}},"connectionId":1131180,"connectionCount":23}}`
	// An authentication line.
	mongodAuth = `{"t":{"$date":"2026-11-19T22:45:01.000+00:00"},"s":"I","c":"ACCESS","id":20250,"ctx":"conn1131181","msg":"Authentication succeeded","attr":{"mechanism":"SCRAM-SHA-256","speculative":true,"principalName":"__system","authenticationDatabase":"local","remote":"192.0.2.10:53720","extraInfo":{}}}`
)

func TestMongod_ConnectionLine_GivesTheMessageWithTheRemoteAndTheCount(t *testing.T) {
	h := newHarness(t, defaultConfig())

	lr, scope := h.runScoped(input{body: mongodEnded})

	wantBody(t, lr, "Connection ended remote=192.0.2.10:53714 connectionCount=23")
	wantSeverity(t, lr, plog.SeverityNumberInfo, "INFO")
	wantTime(t, lr, time.Date(2026, 11, 19, 22, 45, 0, 123000000, time.UTC))
	wantScope(t, scope, "NETWORK")
	a := lr.Attributes()
	attrInt(t, a, "vstan.mongod.id", 22944)
	attrInt(t, a, "client.port", 53714)
	attrInt(t, a, "vstan.mongod.connection_count", 23)
	attrInt(t, a, "vstan.mongod.connection_id", 1131180)
	if attrStr(t, a, "vstan.mongod.ctx") != "conn1131180" || attrStr(t, a, "client.address") != "192.0.2.10" || attrStr(t, a, "log.record.original") != mongodEnded {
		t.Fatalf("attributes = %v", a.AsRaw())
	}
	absent(t, a, "t", "s", "c", "id", "ctx", "msg", "attr", "remote")
}

func TestMongod_AuthenticationLine_GivesThePrincipalAndTheRemote(t *testing.T) {
	h := newHarness(t, defaultConfig())

	lr, scope := h.runScoped(input{body: mongodAuth})

	wantBody(t, lr, "Authentication succeeded principal=__system remote=192.0.2.10:53720")
	wantSeverity(t, lr, plog.SeverityNumberInfo, "INFO")
	wantScope(t, scope, "ACCESS")
	a := lr.Attributes()
	if attrStr(t, a, "user.name") != "__system" || attrStr(t, a, "vstan.mongod.mechanism") != "SCRAM-SHA-256" {
		t.Fatalf("attributes = %v", a.AsRaw())
	}
	absent(t, a, "vstan.mongod.principalName")
}

func TestMongod_SeverityLetters_MapOnTheMongodTable(t *testing.T) {
	want := map[string]plog.SeverityNumber{
		"W": plog.SeverityNumberWarn, "E": plog.SeverityNumberError, "F": plog.SeverityNumberFatal, "D2": plog.SeverityNumberDebug,
	}
	for letter, num := range want {
		h := newHarness(t, defaultConfig())
		lr, _ := h.runScoped(input{body: `{"t":{"$date":"2026-11-19T22:45:00.000+00:00"},"s":"` + letter + `","c":"CONTROL","id":1,"ctx":"main","msg":"m"}`})
		if lr.SeverityNumber() != num {
			t.Fatalf("severity %s -> %d, want %d", letter, lr.SeverityNumber(), num)
		}
	}
}

func TestMongod_LineWithoutAttr_GivesTheMessageOnly(t *testing.T) {
	h := newHarness(t, defaultConfig())

	lr, _ := h.runScoped(input{body: `{"t":{"$date":"2026-11-19T22:45:00.000+00:00"},"s":"I","c":"CONTROL","id":23285,"ctx":"main","msg":"Automatically disabling TLS 1.0"}`})

	wantBody(t, lr, "Automatically disabling TLS 1.0")
}

func TestMongod_BadDate_IsUnchangedAndCounted(t *testing.T) {
	h := newHarness(t, defaultConfig())
	const bad = `{"t":{"$date":"never"},"s":"I","c":"CONTROL","id":1,"ctx":"main","msg":"m"}`

	lr, _ := h.runScoped(input{body: bad})

	wantUnchanged(t, lr, bad, map[string]any{})
	wantFailed(t, h, "mongod")
}
