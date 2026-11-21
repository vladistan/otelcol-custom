// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package redis

import (
	"testing"
	"time"
)

func TestParseRedisLog_KeepsTheTimestampOfTheLine(t *testing.T) {
	info := ParseRedisLog("1:M 19 Nov 2026 23:22:37.151 * Background saving terminated with success")
	if info == nil || info.Timestamp != "19 Nov 2026 23:22:37.151" {
		t.Fatalf("info = %+v", info)
	}
}

func TestLineTime_ReadsDayMonthYearAndTime(t *testing.T) {
	got, ok := LineTime("19 Nov 2026 23:22:37.151")
	want := time.Date(2026, 11, 19, 23, 22, 37, 151000000, time.UTC)
	if !ok || !got.Equal(want) {
		t.Fatalf("LineTime = %v, %v; want %v", got, ok, want)
	}
	if got, ok := LineTime("1 Jan 2026 00:00:00.000"); !ok || got.Day() != 1 {
		t.Fatalf("one digit day: %v %v", got, ok)
	}
	if _, ok := LineTime("32 Foo 2026 25:00:00.000"); ok {
		t.Fatal("a bad timestamp must not parse")
	}
}
