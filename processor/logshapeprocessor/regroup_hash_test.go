// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"fmt"
	"testing"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
)

// peerBatch has one record for each peer name and one scope name for each logger name.
func peerBatch(peers, loggers []string) plog.Logs {
	ld := plog.NewLogs()
	sl := ld.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty()
	for i := range peers {
		lr := sl.LogRecords().AppendEmpty()
		lr.Body().SetStr(fmt.Sprint("record-", i))
		lr.Attributes().PutStr("logger", loggers[i])
		if peers[i] != "" {
			lr.Attributes().PutStr("peer", peers[i])
		}
	}
	return ld
}

func repeated(value string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = value
	}
	return out
}

func numbered(prefix string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprint(prefix, i)
	}
	return out
}

func runTagged(t *testing.T, in plog.Logs) plog.Logs {
	t.Helper()
	h := newHarness(t, defaultConfig())
	h.proc.setBranches([]branch{tagBranch{}})
	return h.runBatch(in)
}

func TestGroups_RecordsWithTheSameChangedResourceShareOneResourceLogs(t *testing.T) {
	out := runTagged(t, peerBatch(repeated("h1", 200), repeated("a", 200)))

	if out.ResourceLogs().Len() != 1 || out.ResourceLogs().At(0).ScopeLogs().Len() != 1 {
		t.Fatalf("resources %d, want 1 with 1 scope", out.ResourceLogs().Len())
	}
	if got := out.ResourceLogs().At(0).ScopeLogs().At(0).LogRecords().Len(); got != 200 {
		t.Errorf("records = %d, want 200", got)
	}
}

func TestGroups_DistinctResourcesStaySeparateInFirstSeenOrder(t *testing.T) {
	out := runTagged(t, peerBatch(numbered("host-", 300), repeated("a", 300)))

	if out.ResourceLogs().Len() != 300 {
		t.Fatalf("resources = %d, want 300", out.ResourceLogs().Len())
	}
	for i := range 300 {
		host, _ := out.ResourceLogs().At(i).Resource().Attributes().Get("host.name")
		if host.Str() != fmt.Sprint("host-", i) {
			t.Fatalf("resource %d has host %q: first-seen order is broken", i, host.Str())
		}
	}
}

func TestGroups_ResourcesThatHashAlikeStillStaySeparate(t *testing.T) {
	was := hashResource
	hashResource = func(pcommon.Map) uint64 { return 7 }
	t.Cleanup(func() { hashResource = was })

	out := runTagged(t, peerBatch([]string{"h1", "h2", "h1", "h3"}, repeated("a", 4)))

	if out.ResourceLogs().Len() != 3 {
		t.Fatalf("resources = %d, want 3 (h1, h2, h3)", out.ResourceLogs().Len())
	}
}

func TestGroups_ScopesThatHashAlikeStillStaySeparate(t *testing.T) {
	was := hashScopeOf
	hashScopeOf = func(pcommon.InstrumentationScope) uint64 { return 7 }
	t.Cleanup(func() { hashScopeOf = was })

	out := runTagged(t, peerBatch(repeated("", 4), []string{"a", "b", "a", "c"}))

	scopes := out.ResourceLogs().At(0).ScopeLogs()
	if scopes.Len() != 3 {
		t.Fatalf("scopes = %d, want 3 (a, b, c)", scopes.Len())
	}
}

func TestGroups_TwoRecordsWithTheSameChangedResourceShareAGroupWhenEveryHashCollides(t *testing.T) {
	was := hashResource
	hashResource = func(pcommon.Map) uint64 { return 1 }
	t.Cleanup(func() { hashResource = was })
	ld := peerBatch([]string{"h1", "h1"}, repeated("a", 2))
	ld.ResourceLogs().At(0).Resource().Attributes().PutStr("k", "v")

	out := runTagged(t, ld)

	if out.ResourceLogs().Len() != 1 {
		t.Fatalf("resources = %d, want 1: two records with the same changed resource", out.ResourceLogs().Len())
	}
}
