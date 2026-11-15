// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package fswatchreceiver

import (
	"fmt"
	"testing"
	"time"
)

func TestStateRoundTrip(t *testing.T) {
	sentinel := time.Date(2026, 11, 14, 12, 0, 0, 5, time.UTC)
	recent := []recentEntry{{Path: `d:\a.dx`, Created: sentinel.UnixNano() - 1}}
	raw, err := marshalState(sentinel, recent, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := parseState(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.V != 1 || got.Sentinel != sentinel.UnixNano() || len(got.Recent) != 1 || got.Recent[0] != recent[0] {
		t.Fatalf("got %+v", got)
	}
}

func TestStateJSONShape(t *testing.T) {
	raw, err := marshalState(time.Unix(0, 42), []recentEntry{{Path: "p", Created: 7}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"v":1,"sentinel":42,"recent":[{"p":"p","t":7}]}` {
		t.Fatalf("got %s", raw)
	}
}

func TestDoneRoundTripsAndDropsOldEntries(t *testing.T) {
	floor := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	done := map[seenKey]struct{}{
		{path: "new", created: floor.Add(time.Minute).UnixNano()}:  {},
		{path: "old", created: floor.Add(-time.Minute).UnixNano()}: {},
	}
	entries := pruneDone(done, floor)
	if len(entries) != 1 || entries[0].Path != "new" || len(done) != 1 {
		t.Fatalf("entries %+v, map %d", entries, len(done))
	}
	raw, err := marshalState(floor, nil, entries)
	if err != nil {
		t.Fatal(err)
	}
	got, err := parseState(raw)
	if err != nil || len(got.Done) != 1 || got.Done[0] != entries[0] {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestDoneIsCappedAtNewest(t *testing.T) {
	base := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	done := map[seenKey]struct{}{}
	for i := 0; i < maxDone+10; i++ {
		done[seenKey{path: fmt.Sprintf("p%05d", i), created: base.Add(time.Duration(i) * time.Millisecond).UnixNano()}] = struct{}{}
	}
	entries := pruneDone(done, base)
	if len(entries) != maxDone || len(done) != maxDone || entries[0].Path != fmt.Sprintf("p%05d", maxDone+9) {
		t.Fatalf("entries %d, map %d, first %s", len(entries), len(done), entries[0].Path)
	}
}

func TestParseStateRejects(t *testing.T) {
	for name, raw := range map[string]string{
		"not json":            `{`,
		"unknown version":     `{"v":2,"sentinel":1,"recent":[]}`,
		"missing version":     `{"sentinel":1}`,
		"negative sentinel":   `{"v":1,"sentinel":-5,"recent":[]}`,
		"wrong sentinel type": `{"v":1,"sentinel":"x"}`,
	} {
		if _, err := parseState([]byte(raw)); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func TestRecentIsCappedAtNewest256(t *testing.T) {
	seen := map[seenKey]time.Time{}
	sentinel := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 300; i++ {
		created := sentinel.Add(-time.Duration(i) * time.Millisecond)
		seen[seenKey{path: fmt.Sprintf("p%03d", i), created: created.UnixNano()}] = created
	}
	seen[seenKey{path: "old", created: sentinel.Add(-time.Minute).UnixNano()}] = sentinel.Add(-time.Minute)
	got := recentFrom(seen, sentinel)
	if len(got) != 256 {
		t.Fatalf("got %d entries", len(got))
	}
	for _, e := range got {
		if e.Path == "old" {
			t.Fatal("an entry older than 2 s before the sentinel must be left out")
		}
	}
	if got[0].Path != "p000" {
		t.Fatalf("newest first, got %s", got[0].Path)
	}
}
