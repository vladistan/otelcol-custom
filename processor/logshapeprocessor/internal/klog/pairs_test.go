// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package klog

import "testing"

func TestParseKlog_KeepsThePairsInTheOrderOfTheLine(t *testing.T) {
	info := ParseKlog(`I1119 23:05:39.184859       1 client.go:214] "Connect to server" zeta="1" alpha="two words" mid=3`)
	if info == nil {
		t.Fatal("no parse")
	}
	want := []KV{{"zeta", "1"}, {"alpha", "two words"}, {"mid", "3"}}
	if len(info.Pairs) != len(want) {
		t.Fatalf("pairs = %v", info.Pairs)
	}
	for i, kv := range want {
		if info.Pairs[i] != kv {
			t.Errorf("pair %d = %v, want %v", i, info.Pairs[i], kv)
		}
	}
}

func TestParseKlog_KeepsTheRawMonthDayAndClock(t *testing.T) {
	info := ParseKlog(`W0102 03:04:05.678 7 a.go:9] "x"`)
	if info == nil || info.MonthDay != "0102" || info.Clock != "03:04:05.678" {
		t.Fatalf("info = %+v", info)
	}
}
