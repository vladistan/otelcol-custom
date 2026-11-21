// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logrus

import (
	"testing"
	"time"
)

func TestEpochTime_PicksTheUnitFromTheSizeAndChecksTheRange(t *testing.T) {
	want := time.Date(2026, 11, 20, 4, 40, 2, 123_000_000, time.UTC)
	secs := float64(want.Unix()) + 0.123
	for _, c := range []struct {
		name string
		in   float64
		ok   bool
	}{
		{"seconds with a fraction", secs, true},
		{"milliseconds", float64(want.UnixMilli()), true},
		{"microseconds", float64(want.UnixMicro()), true},
		{"nanoseconds", float64(want.UnixNano()), true},
		{"a small number is not a time", 12, false},
		{"zero", 0, false},
		{"negative", -5, false},
		{"year 1999", 915148800, false},
		{"year 2100 and later", 4102444800, false},
		{"too big for nanoseconds", 9e20, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, ok := EpochTime(c.in)
			if ok != c.ok {
				t.Fatalf("EpochTime(%v) ok = %v, want %v (got %v)", c.in, ok, c.ok, got)
			}
			if c.ok && got.Sub(want).Abs() > time.Millisecond {
				t.Fatalf("EpochTime(%v) = %v, want %v", c.in, got, want)
			}
		})
	}
}

func TestParseLogrusJSON_ReadsEpochAndRFC3339Times(t *testing.T) {
	epoch := ParseLogrusJSON(`{"level":"info","ts":1795129202.123,"msg":"started"}`)
	if epoch == nil || epoch.Timestamp.IsZero() || epoch.Timestamp.Year() != 2026 {
		t.Fatalf("epoch ts: %+v", epoch)
	}
	text := ParseLogrusJSON(`{"level":"info","ts":"2026-01-02T05:00:25.573798Z","msg":"started"}`)
	if text == nil || !text.Timestamp.Equal(time.Date(2026, 1, 2, 5, 0, 25, 573798000, time.UTC)) {
		t.Fatalf("rfc3339 ts: %+v", text)
	}
	tiny := ParseLogrusJSON(`{"level":"info","ts":12,"msg":"started"}`)
	if tiny == nil || !tiny.Timestamp.IsZero() {
		t.Fatalf("a time out of range must stay unset: %+v", tiny)
	}
	if !tiny.TimeBad {
		t.Fatalf("a time out of range must be flagged: %+v", tiny)
	}
}

func TestParseLogrusJSON_ErrorObject_GivesTypeAndMessage(t *testing.T) {
	info := ParseLogrusJSON(`{"level":"error","msg":"call failed","error":{"type":"TimeoutError","message":"deadline exceeded"}}`)
	if info == nil || info.Error != "deadline exceeded" || info.ErrorType != "TimeoutError" {
		t.Fatalf("info = %+v", info)
	}
	plain := ParseLogrusJSON(`{"level":"error","msg":"call failed","error":"boom"}`)
	if plain == nil || plain.Error != "boom" || plain.ErrorType != "" {
		t.Fatalf("info = %+v", plain)
	}
}

func TestParseLogrusJSON_ModuleLoggerAndNumberExtras(t *testing.T) {
	info := ParseLogrusJSON(`{"level":"error","time":"2026-11-19T23:00:12Z","msg":"regen failed","subsys":"agent.datapath","logger":"cilium","nodeID":3,"ok":true,"nested":{"a":1}}`)
	if info == nil {
		t.Fatal("no parse")
	}
	if info.Module != "agent.datapath" || info.Logger != "cilium" {
		t.Errorf("module %q logger %q", info.Module, info.Logger)
	}
	if info.Extra["nodeID"] != "3" || info.Extra["ok"] != "true" {
		t.Errorf("extra = %v", info.Extra)
	}
	if _, ok := info.Extra["nested"]; ok {
		t.Errorf("a nested object must not become an attribute: %v", info.Extra)
	}
	if _, ok := info.Extra["subsys"]; ok {
		t.Errorf("subsys is the module, not an extra: %v", info.Extra)
	}
}

func TestParseLogrusJSON_ServiceAndHostnameAreDropped(t *testing.T) {
	info := ParseLogrusJSON(`{"level":"info","ts":"2026-01-02T05:00:25Z","msg":"x","service":"host-sync-bot","hostname":"h1","version":"1.2"}`)
	if info == nil {
		t.Fatal("no parse")
	}
	for _, k := range []string{"service", "hostname"} {
		if _, ok := info.Extra[k]; ok {
			t.Errorf("%s must be dropped: %v", k, info.Extra)
		}
	}
	if info.Extra["version"] != "1.2" {
		t.Errorf("extra = %v", info.Extra)
	}
}
