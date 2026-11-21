// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package postgres

import (
	"testing"
	"time"
)

func TestLineTime_ReadsTheTimestampInItsZone(t *testing.T) {
	for _, c := range []struct {
		name, ts, zone string
		want           time.Time
		ok             bool
	}{
		{"utc", "2026-11-19 22:42:20.918", "UTC", time.Date(2026, 11, 19, 22, 42, 20, 918000000, time.UTC), true},
		{"gmt", "2026-11-19 22:42:20.918", "GMT", time.Date(2026, 11, 19, 22, 42, 20, 918000000, time.UTC), true},
		{"central european winter", "2026-11-19 22:42:20.918", "CET", time.Date(2026, 11, 19, 21, 42, 20, 918000000, time.UTC), true},
		{"central european summer", "2026-07-19 22:42:20.918", "CEST", time.Date(2026, 7, 19, 20, 42, 20, 918000000, time.UTC), true},
		{"unknown zone is not guessed", "2026-11-19 22:42:20.918", "XYZ", time.Time{}, false},
		{"bad timestamp", "2026-13-45 99:00:00.000", "UTC", time.Time{}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, ok := LineTime(c.ts, c.zone)
			if ok != c.ok || !got.Equal(c.want) {
				t.Fatalf("LineTime(%q, %q) = %v, %v; want %v, %v", c.ts, c.zone, got, ok, c.want, c.ok)
			}
		})
	}
}

func TestParsePostgresLog_KeepsTheTimestampAndZone(t *testing.T) {
	info := ParsePostgresLog("2026-11-19 22:42:20.918 UTC [815116] LOG:  deleted 0 row(s)")
	if info == nil || info.Timestamp != "2026-11-19 22:42:20.918" || info.Zone != "UTC" {
		t.Fatalf("info = %+v", info)
	}
}
