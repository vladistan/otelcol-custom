// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package auth0receiver

import (
	"testing"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
)

func mapOne(t *testing.T, body string) (pcommon.Map, plog.LogRecord) {
	t.Helper()
	env, err := decodeEnvelope(body, defaultSuffixes)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	logs, err := mapEnvelope(env)
	if err != nil {
		t.Fatalf("map: %v", err)
	}
	if logs.LogRecordCount() != 1 {
		t.Fatalf("records = %d, want 1", logs.LogRecordCount())
	}
	rl := logs.ResourceLogs().At(0)
	return rl.Resource().Attributes(), rl.ScopeLogs().At(0).LogRecords().At(0)
}

func wantStr(t *testing.T, m pcommon.Map, key, want string) {
	t.Helper()
	got, ok := m.Get(key)
	if !ok || got.Str() != want {
		t.Fatalf("%s = %q (present=%v), want %q", key, got.Str(), ok, want)
	}
}

func TestMapLiveFailureEvent(t *testing.T) {
	res, rec := mapOne(t, sampleBody(t, 0))
	wantStr(t, res, "service.name", "auth0")
	wantStr(t, res, "cloud.account.id", "111122223333")
	wantStr(t, res, "cloud.region", "eu-central-1")

	if got, want := rec.Body().Str(), "Unknown client: CLIENTID00000000000000EXAMPLE"; got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
	if got, want := rec.Timestamp().AsTime(), time.Date(2026, 10, 25, 14, 32, 40, 317000000, time.UTC); !got.Equal(want) {
		t.Fatalf("timestamp = %v, want %v", got, want)
	}
	attrs := rec.Attributes()
	wantStr(t, attrs, "log.source", "auth0")
	wantStr(t, attrs, "event.category", "authentication")
	wantStr(t, attrs, "auth0.type", "f")
	wantStr(t, attrs, "auth0.log_id", "90020261025143240391299000000000000001223372079833344945")
	wantStr(t, attrs, "event.id", "90020261025143240391299000000000000001223372079833344945")
	wantStr(t, attrs, "client.address", "192.0.2.10")
	wantStr(t, attrs, "user_agent.original", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/78.0.3904.108 Safari/537.36")
	if _, ok := attrs.Get("original_message"); !ok {
		t.Fatal("original_message missing")
	}
	for _, absent := range []string{"auth0.client_name", "auth0.connection", "user.id", "user.name"} {
		if _, ok := attrs.Get(absent); ok {
			t.Fatalf("%s set although the event lacks it", absent)
		}
	}
	if rec.SeverityNumber() != plog.SeverityNumberError {
		t.Fatalf("severity = %v, want ERROR", rec.SeverityNumber())
	}
}

func TestMapTimestampFallsBackToEnvelopeTime(t *testing.T) {
	_, rec := mapOne(t, `{"source":"aws.partner/auth0.com/t/auth0.logs","time":"2026-10-25T14:32:40Z","detail":{"data":{"type":"s","description":"ok"}}}`)
	if got, want := rec.Timestamp().AsTime(), time.Date(2026, 10, 25, 14, 32, 40, 0, time.UTC); !got.Equal(want) {
		t.Fatalf("timestamp = %v, want %v", got, want)
	}
}

func TestMapMissingDescriptionGivesTypeBody(t *testing.T) {
	_, rec := mapOne(t, `{"source":"aws.partner/auth0.com/t/auth0.events","detail":{"data":{"type":"seacft"}}}`)
	if got := rec.Body().Str(); got != "auth0 seacft" {
		t.Fatalf("body = %q", got)
	}
}

func TestMapSeverityByTypeCode(t *testing.T) {
	tests := map[string]plog.SeverityNumber{
		"s":      plog.SeverityNumberInfo,
		"seacft": plog.SeverityNumberInfo,
		"f":      plog.SeverityNumberError,
		"fp":     plog.SeverityNumberError,
		"w":      plog.SeverityNumberWarn,
		"unseen": plog.SeverityNumberInfo,
		"":       plog.SeverityNumberInfo,
	}
	for code, want := range tests {
		t.Run(code, func(t *testing.T) {
			_, rec := mapOne(t, `{"source":"aws.partner/auth0.com/t/auth0.logs","detail":{"data":{"type":"`+code+`"}}}`)
			if rec.SeverityNumber() != want {
				t.Fatalf("type %q severity = %v, want %v", code, rec.SeverityNumber(), want)
			}
		})
	}
}

func TestMapReconstructedSamplesMapSeverityOnly(t *testing.T) {
	for i, want := range map[int]plog.SeverityNumber{1: plog.SeverityNumberInfo, 2: plog.SeverityNumberInfo} {
		_, rec := mapOne(t, sampleBody(t, i))
		if rec.SeverityNumber() != want {
			t.Fatalf("sample %d severity = %v, want %v", i, rec.SeverityNumber(), want)
		}
	}
}

func TestMapOptionalUserFields(t *testing.T) {
	_, rec := mapOne(t, `{"source":"aws.partner/auth0.com/t/auth0.logs","detail":{"data":{"type":"s","user_id":"auth0|1","user_name":"u@example.com","client_name":"App","connection":"db"}}}`)
	attrs := rec.Attributes()
	wantStr(t, attrs, "user.id", "auth0|1")
	wantStr(t, attrs, "user.name", "u@example.com")
	wantStr(t, attrs, "auth0.client_name", "App")
	wantStr(t, attrs, "auth0.connection", "db")
}

func TestMapRejectsDetailWithoutData(t *testing.T) {
	env, err := decodeEnvelope(`{"source":"aws.partner/auth0.com/t/auth0.logs","detail":{"log_id":"1"}}`, defaultSuffixes)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, err := mapEnvelope(env); err == nil {
		t.Fatal("mapEnvelope accepted a detail without data")
	}
}
