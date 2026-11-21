// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"errors"
	"testing"

	"go.opentelemetry.io/collector/pdata/plog"
)

// flakyBranch claims records with an "x" attribute. It writes the resource before it can fail, sets the
// scope name only when the record has "scope", and fails when the record has "fail".
type flakyBranch struct{}

func (flakyBranch) name() string { return "flaky" }

func (flakyBranch) claims(r *rec) bool {
	_, ok := r.lr.Attributes().Get("x")
	return ok
}

func (flakyBranch) parse(r *rec) error {
	attrs := r.lr.Attributes()
	if _, ok := attrs.Get("fail"); ok {
		r.res.Attributes().PutStr("host.name", "partial")
		r.scope.SetName("partial")
		attrs.PutStr("half", "written")
		return errors.New("bad line")
	}
	if peer, ok := attrs.Get("peer"); ok {
		r.res.Attributes().PutStr("host.name", peer.Str())
	}
	if name, ok := attrs.Get("scope"); ok {
		r.scope.SetName(name.Str())
	}
	return nil
}

// flakyBatch has one record for each spec, in order, all under one resource and one scope named base.
func flakyBatch(specs ...map[string]string) plog.Logs {
	ld := plog.NewLogs()
	rl := ld.ResourceLogs().AppendEmpty()
	rl.Resource().Attributes().PutStr("service.name", "svc")
	sl := rl.ScopeLogs().AppendEmpty()
	sl.Scope().SetName("base")
	for i, spec := range specs {
		lr := sl.LogRecords().AppendEmpty()
		lr.Body().SetStr(string(rune('a' + i)))
		lr.Attributes().PutStr("x", "1")
		for k, v := range spec {
			lr.Attributes().PutStr(k, v)
		}
	}
	return ld
}

func runFlaky(t *testing.T, in plog.Logs) (plog.Logs, *harness) {
	t.Helper()
	h := newHarness(t, defaultConfig())
	h.proc.setBranches([]branch{flakyBranch{}})
	return h.runBatch(in), h
}

func TestWork_AResourceChangedForOneRecordDoesNotLeakIntoTheNextRecord(t *testing.T) {
	out, _ := runFlaky(t, flakyBatch(map[string]string{"peer": "h1"}, nil, map[string]string{"peer": "h1"}))

	order, bodies := groupsOf(out)

	want := map[groupView][]string{{"svc", "h1", "base"}: {"a", "c"}, {"svc", "", "base"}: {"b"}}
	if len(order) != 2 || len(bodies[groupView{"svc", "", "base"}]) != 1 {
		t.Fatalf("groups = %v, want %v", bodies, want)
	}
}

func TestWork_AScopeChangedForOneRecordDoesNotLeakIntoTheNextRecord(t *testing.T) {
	out, _ := runFlaky(t, flakyBatch(map[string]string{"scope": "s1"}, nil, map[string]string{"scope": "s1"}))

	_, bodies := groupsOf(out)

	if got := bodies[groupView{"svc", "", "base"}]; len(got) != 1 || got[0] != "b" {
		t.Fatalf("records that kept the scope base = %v, want [b]; groups %v", got, bodies)
	}
}

func TestWork_AFailedParseLeavesNeitherTheRecordNorTheNextRecordChanged(t *testing.T) {
	out, h := runFlaky(t, flakyBatch(map[string]string{"fail": "1"}, nil))

	_, bodies := groupsOf(out)

	if got := bodies[groupView{"svc", "", "base"}]; len(got) != 2 {
		t.Fatalf("both records must stay under the input resource and scope, groups %v", bodies)
	}
	if _, ok := out.ResourceLogs().At(0).ScopeLogs().At(0).LogRecords().At(0).Attributes().Get("half"); ok {
		t.Error("the failed record kept an attribute its parse wrote")
	}
	if got := h.counter(parseFailedMetric, "format"); got["flaky"] != 1 {
		t.Errorf("parse_failed = %v, want one for flaky", got)
	}
}

func TestWork_ARecordAfterAFailedParseIsParsedFromTheInputAgain(t *testing.T) {
	out, _ := runFlaky(t, flakyBatch(map[string]string{"fail": "1"}, map[string]string{"peer": "h2"}))

	_, bodies := groupsOf(out)

	if len(bodies[groupView{"svc", "partial", "base"}]) != 0 || len(bodies[groupView{"svc", "h2", "base"}]) != 1 {
		t.Fatalf("groups = %v: the second record must carry h2 and nothing of the failed parse", bodies)
	}
}
