// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"testing"

	"go.opentelemetry.io/collector/pdata/plog"
)

// tagBranch claims records with a "logger" attribute. It sets the scope name from it and the host from "peer".
type tagBranch struct{}

func (tagBranch) name() string { return "tag" }

func (tagBranch) claims(r *rec) bool {
	_, ok := r.lr.Attributes().Get("logger")
	return ok
}

func (tagBranch) parse(r *rec) error {
	logger, _ := r.lr.Attributes().Get("logger")
	r.scope.SetName(logger.Str())
	if peer, ok := r.lr.Attributes().Get("peer"); ok {
		r.res.Attributes().PutStr("host.name", peer.Str())
	}
	r.lr.Attributes().Remove("logger")
	r.lr.Attributes().Remove("peer")
	return nil
}

func mixedBatch() plog.Logs {
	ld := plog.NewLogs()
	for _, svc := range []string{"s0", "s1"} {
		rl := ld.ResourceLogs().AppendEmpty()
		rl.Resource().Attributes().PutStr("service.name", svc)
		sl := rl.ScopeLogs().AppendEmpty()
		sl.Scope().SetName("base")
		sl.Scope().SetVersion("1.0")
		for i, c := range []struct{ logger, peer string }{{"a", "h1"}, {"b", "h2"}, {"a", "h1"}, {"", ""}} {
			lr := sl.LogRecords().AppendEmpty()
			lr.Body().SetStr(svc + "-" + string(rune('0'+i)))
			if c.logger != "" {
				lr.Attributes().PutStr("logger", c.logger)
				lr.Attributes().PutStr("peer", c.peer)
			}
		}
	}
	return ld
}

type groupView struct{ service, host, scope string }

func groupsOf(ld plog.Logs) (order []groupView, bodies map[groupView][]string) {
	bodies = map[groupView][]string{}
	for i := range ld.ResourceLogs().Len() {
		rl := ld.ResourceLogs().At(i)
		ra := rl.Resource().Attributes().AsRaw()
		for j := range rl.ScopeLogs().Len() {
			sl := rl.ScopeLogs().At(j)
			g := groupView{scope: sl.Scope().Name()}
			g.service, _ = ra["service.name"].(string)
			g.host, _ = ra["host.name"].(string)
			order = append(order, g)
			for k := range sl.LogRecords().Len() {
				bodies[g] = append(bodies[g], sl.LogRecords().At(k).Body().Str())
			}
		}
	}
	return order, bodies
}

func TestRegroup_MixedBatch_EachRecordLandsUnderItsOwnScopeAndResource(t *testing.T) {
	h := newHarness(t, defaultConfig())
	h.proc.setBranches([]branch{tagBranch{}})

	_, bodies := groupsOf(h.runBatch(mixedBatch()))

	want := map[groupView][]string{
		{"s0", "h1", "a"}: {"s0-0", "s0-2"}, {"s0", "h2", "b"}: {"s0-1"}, {"s0", "", "base"}: {"s0-3"},
		{"s1", "h1", "a"}: {"s1-0", "s1-2"}, {"s1", "h2", "b"}: {"s1-1"}, {"s1", "", "base"}: {"s1-3"},
	}
	if len(bodies) != len(want) {
		t.Fatalf("%d groups, want %d: %v", len(bodies), len(want), bodies)
	}
	for g, w := range want {
		got := bodies[g]
		if len(got) != len(w) {
			t.Errorf("group %+v = %v, want %v", g, got, w)
			continue
		}
		for i := range w {
			if got[i] != w[i] {
				t.Errorf("group %+v = %v, want %v (order)", g, got, w)
			}
		}
	}
}

func TestRegroup_GroupsAppearInFirstSeenOrder(t *testing.T) {
	h := newHarness(t, defaultConfig())
	h.proc.setBranches([]branch{tagBranch{}})

	order, _ := groupsOf(h.runBatch(mixedBatch()))

	want := []groupView{
		{"s0", "h1", "a"}, {"s0", "h2", "b"}, {"s0", "", "base"},
		{"s1", "h1", "a"}, {"s1", "h2", "b"}, {"s1", "", "base"},
	}
	if len(order) != len(want) {
		t.Fatalf("order = %v", order)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Errorf("group %d = %+v, want %+v", i, order[i], want[i])
		}
	}
}

func TestRegroup_TheSharedInputScopeAndResourceAreNeverChanged(t *testing.T) {
	h := newHarness(t, defaultConfig())
	h.proc.setBranches([]branch{tagBranch{}})
	in := mixedBatch()
	rl0 := in.ResourceLogs().At(0)
	res0, scope0 := rl0.Resource(), rl0.ScopeLogs().At(0).Scope()

	h.runBatch(in)

	if res0.Attributes().Len() != 1 {
		t.Errorf("the input resource changed: %v", res0.Attributes().AsRaw())
	}
	if scope0.Name() != "base" || scope0.Version() != "1.0" {
		t.Errorf("the input scope changed: %q %q", scope0.Name(), scope0.Version())
	}
}

func TestRegroup_AChangedScopeKeepsTheVersionOfTheOriginal(t *testing.T) {
	h := newHarness(t, defaultConfig())
	h.proc.setBranches([]branch{tagBranch{}})

	out := h.runBatch(mixedBatch())

	for i := range out.ResourceLogs().Len() {
		for j := range out.ResourceLogs().At(i).ScopeLogs().Len() {
			if v := out.ResourceLogs().At(i).ScopeLogs().At(j).Scope().Version(); v != "1.0" {
				t.Errorf("scope version = %q, want 1.0", v)
			}
		}
	}
}

func TestRegroup_ClaimedRecordsThatChangeNothing_StayInOneResourceAndScope(t *testing.T) {
	h := newHarness(t, defaultConfig())
	ld := plog.NewLogs()
	rl := ld.ResourceLogs().AppendEmpty()
	rl.Resource().Attributes().PutStr("k", "v")
	sl := rl.ScopeLogs().AppendEmpty()
	sl.Scope().SetName("base")
	for range 3 {
		sl.LogRecords().AppendEmpty().Body().SetStr(klogLine)
	}

	out := h.runBatch(ld)

	if out.ResourceLogs().Len() != 1 || out.ResourceLogs().At(0).ScopeLogs().Len() != 1 {
		t.Fatalf("resources %d, scopes %d", out.ResourceLogs().Len(), out.ResourceLogs().At(0).ScopeLogs().Len())
	}
	if out.ResourceLogs().At(0).ScopeLogs().At(0).Scope().Name() != "base" {
		t.Errorf("scope name = %q", out.ResourceLogs().At(0).ScopeLogs().At(0).Scope().Name())
	}
}

func TestRegroup_CountersAreUnchangedByTheRegroup(t *testing.T) {
	h := newHarness(t, defaultConfig())
	h.proc.setBranches([]branch{tagBranch{}})
	ld := mixedBatch()

	h.runBatch(ld)

	if got := h.counter(unmatchedMetric, "source"); got["none"] != 2 {
		t.Errorf("unmatched = %v, want 2 (the two records with no logger)", got)
	}
	if got := h.counter(parseFailedMetric, "format"); len(got) != 0 {
		t.Errorf("parse_failed = %v", got)
	}
}

// BenchmarkBuildOnly builds the same batch without the processor; subtract it from BenchmarkConsumeUnmatched.
func BenchmarkBuildOnly(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		ld := plog.NewLogs()
		sl := ld.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty()
		for range 1000 {
			sl.LogRecords().AppendEmpty().Body().SetStr("plain line that no parser claims")
		}
	}
}

func BenchmarkConsumeUnmatched(b *testing.B) {
	p := newHarnessB(b, defaultConfig())
	b.ReportAllocs()
	for b.Loop() {
		ld := plog.NewLogs()
		sl := ld.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty()
		for range 1000 {
			sl.LogRecords().AppendEmpty().Body().SetStr("plain line that no parser claims")
		}
		if err := p.ConsumeLogs(b.Context(), ld); err != nil {
			b.Fatal(err)
		}
	}
}
