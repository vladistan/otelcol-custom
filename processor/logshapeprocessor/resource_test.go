// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"errors"
	"testing"

	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/pdata/plog"
)

// hostBranch claims records that carry a "peer" attribute and moves it to host.name.
type hostBranch struct{ fail bool }

func (hostBranch) name() string { return "host" }

func (hostBranch) claims(r *rec) bool {
	_, ok := r.lr.Attributes().Get("peer")
	return ok
}

func (b hostBranch) parse(r *rec) error {
	if b.fail {
		r.res.Attributes().PutStr("host.name", "partial")
		return errors.New("bad")
	}
	peer, _ := r.lr.Attributes().Get("peer")
	r.res.Attributes().PutStr("host.name", peer.Str())
	r.lr.Attributes().Remove("peer")
	return nil
}

func hostProcessor(t *testing.T, fail bool) (*shapeProcessor, *consumertest.LogsSink) {
	t.Helper()
	h := newHarness(t, defaultConfig())
	h.proc.setBranches([]branch{hostBranch{fail: fail}})
	return h.proc, h.sink
}

func batch(peers ...string) plog.Logs {
	ld := plog.NewLogs()
	rl := ld.ResourceLogs().AppendEmpty()
	rl.Resource().Attributes().PutStr("service.name", "syslog")
	sl := rl.ScopeLogs().AppendEmpty()
	sl.Scope().SetName("scope-a")
	for _, p := range peers {
		lr := sl.LogRecords().AppendEmpty()
		lr.Body().SetStr("line " + p)
		if p != "" {
			lr.Attributes().PutStr("peer", p)
		}
	}
	return ld
}

func hostsOf(ld plog.Logs) map[string][]string {
	got := map[string][]string{}
	for i := range ld.ResourceLogs().Len() {
		rl := ld.ResourceLogs().At(i)
		host := ""
		if v, ok := rl.Resource().Attributes().Get("host.name"); ok {
			host = v.Str()
		}
		for j := range rl.ScopeLogs().Len() {
			sl := rl.ScopeLogs().At(j)
			for k := range sl.LogRecords().Len() {
				got[host] = append(got[host], sl.LogRecords().At(k).Body().Str())
			}
		}
	}
	return got
}

func TestConsume_BranchThatSetsTheHost_GivesEachRecordItsOwnResource(t *testing.T) {
	proc, sink := hostProcessor(t, false)

	if err := proc.ConsumeLogs(t.Context(), batch("sw-1", "sw-2", "sw-1", "")); err != nil {
		t.Fatal(err)
	}

	got := hostsOf(sink.AllLogs()[0])
	if len(got["sw-1"]) != 2 || len(got["sw-2"]) != 1 || len(got[""]) != 1 {
		t.Fatalf("records by host = %v", got)
	}
	if got["sw-1"][0] != "line sw-1" || got["sw-1"][1] != "line sw-1" {
		t.Fatalf("records by host = %v", got)
	}
}

func TestConsume_ResourceSplit_KeepsTheOtherResourceAttributesAndTheScope(t *testing.T) {
	proc, sink := hostProcessor(t, false)

	if err := proc.ConsumeLogs(t.Context(), batch("sw-1", "sw-2")); err != nil {
		t.Fatal(err)
	}

	out := sink.AllLogs()[0]
	if out.ResourceLogs().Len() != 2 {
		t.Fatalf("resources = %d, want 2", out.ResourceLogs().Len())
	}
	for i := range out.ResourceLogs().Len() {
		rl := out.ResourceLogs().At(i)
		if v, _ := rl.Resource().Attributes().Get("service.name"); v.Str() != "syslog" {
			t.Errorf("resource %d lost service.name: %v", i, rl.Resource().Attributes().AsRaw())
		}
		if rl.ScopeLogs().At(0).Scope().Name() != "scope-a" {
			t.Errorf("resource %d lost the scope name", i)
		}
	}
}

func TestConsume_BranchThatFailsAfterSettingTheHost_LeavesTheResourceAlone(t *testing.T) {
	proc, sink := hostProcessor(t, true)

	if err := proc.ConsumeLogs(t.Context(), batch("sw-1", "sw-2")); err != nil {
		t.Fatal(err)
	}

	got := hostsOf(sink.AllLogs()[0])
	if len(got) != 1 || len(got[""]) != 2 {
		t.Fatalf("a failed parse changed the resource: %v", got)
	}
	out := sink.AllLogs()[0].ResourceLogs().At(0)
	if _, ok := out.Resource().Attributes().Get("host.name"); ok {
		t.Fatal("partial host.name written")
	}
	if out.ScopeLogs().At(0).LogRecords().At(0).Attributes().Len() != 1 {
		t.Fatal("record attributes changed by a failed parse")
	}
}

func TestConsume_UnchangedResource_StaysOneResourceLogs(t *testing.T) {
	h := newHarness(t, defaultConfig())
	ld := plog.NewLogs()
	rl := ld.ResourceLogs().AppendEmpty()
	rl.Resource().Attributes().PutStr("k", "v")
	sl := rl.ScopeLogs().AppendEmpty()
	for _, b := range []string{"a", "b", "c"} {
		sl.LogRecords().AppendEmpty().Body().SetStr(b)
	}

	if err := h.proc.ConsumeLogs(t.Context(), ld); err != nil {
		t.Fatal(err)
	}

	out := h.sink.AllLogs()[0]
	if out.ResourceLogs().Len() != 1 || out.ResourceLogs().At(0).ScopeLogs().At(0).LogRecords().Len() != 3 {
		t.Fatalf("resources %d", out.ResourceLogs().Len())
	}
	if v, _ := out.ResourceLogs().At(0).Resource().Attributes().Get("k"); v.Str() != "v" {
		t.Fatal("resource attribute lost")
	}
}
