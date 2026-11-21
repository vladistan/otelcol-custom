// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"maps"
	"strings"
	"testing"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/processor/processortest"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// boomBranch claims a body that holds "boom" and panics where it is told to.
type boomBranch struct {
	name_ string
	where string // "claims" or "parse"
}

func (b boomBranch) name() string { return b.name_ }

func (b boomBranch) claims(r *rec) bool {
	if !strings.Contains(r.body, "boom") {
		return false
	}
	if b.where == "claims" {
		var s []int
		_ = s[5] // an index out of range, like a bad slice bound
	}
	return true
}

func (b boomBranch) parse(r *rec) error {
	r.lr.Attributes().PutStr("partial", "written before the panic")
	r.res.Attributes().PutStr("host.name", "partial")
	r.lr.Body().SetStr("changed")
	panic("parse blew up")
}

func harnessWithLog(t *testing.T) (*harness, *observer.ObservedLogs) {
	t.Helper()
	core, logs := observer.New(zapcore.DebugLevel)
	reader := sdkmetric.NewManualReader()
	set := processortest.NewNopSettings(component.MustNewType(typeStr))
	set.MeterProvider = sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	set.Logger = zap.New(core)
	sink := new(consumertest.LogsSink)
	proc, err := newProcessor(set, defaultConfig(), sink)
	if err != nil {
		t.Fatal(err)
	}
	return &harness{t: t, proc: proc, sink: sink, reader: reader}, logs
}

func batchWith(bodies ...string) plog.Logs {
	ld := plog.NewLogs()
	rl := ld.ResourceLogs().AppendEmpty()
	rl.Resource().Attributes().PutStr("service.name", "svc")
	sl := rl.ScopeLogs().AppendEmpty()
	for _, b := range bodies {
		sl.LogRecords().AppendEmpty().Body().SetStr(b)
	}
	return ld
}

func TestRecover_PanicInParse_LeavesTheRecordUnchangedAndCountsAFailure(t *testing.T) {
	h, _ := harnessWithLog(t)
	h.proc.setBranches([]branch{boomBranch{name_: "boom", where: "parse"}})

	out := flatten(h.runBatch(batchWith("a boom line")))

	if len(out) != 1 || out[0].body != "a boom line" || len(out[0].attrs) != 0 {
		t.Fatalf("the record changed: %+v", out)
	}
	if _, ok := out[0].resource["host.name"]; ok {
		t.Errorf("a partial resource write survived: %v", out[0].resource)
	}
	if got := h.counter(parseFailedMetric, "format"); !maps.Equal(got, map[string]int64{"boom": 1}) {
		t.Errorf("parse_failed = %v", got)
	}
	if got := h.counter(unmatchedMetric, "source"); len(got) != 0 {
		t.Errorf("a panicked record is not unmatched: %v", got)
	}
}

func TestRecover_PanicInClaims_LeavesTheRecordUnchangedAndCountsAFailure(t *testing.T) {
	h, _ := harnessWithLog(t)
	h.proc.setBranches([]branch{boomBranch{name_: "boom", where: "claims"}})

	out := flatten(h.runBatch(batchWith("a boom line")))

	if len(out) != 1 || out[0].body != "a boom line" || len(out[0].attrs) != 0 {
		t.Fatalf("the record changed: %+v", out)
	}
	if got := h.counter(parseFailedMetric, "format"); !maps.Equal(got, map[string]int64{"boom": 1}) {
		t.Errorf("parse_failed = %v", got)
	}
}

func TestRecover_TheRestOfTheBatchIsStillShaped(t *testing.T) {
	h, _ := harnessWithLog(t)
	h.proc.setBranches(append([]branch{boomBranch{name_: "boom", where: "parse"}}, h.proc.branches...))

	out := flatten(h.runBatch(batchWith(klogLine, "a boom line", "plain")))

	bodies := map[string]bool{}
	for _, r := range out {
		bodies[r.body] = true
	}
	if !bodies["a boom line"] || !bodies["plain"] || !bodies["Connect to server serverID=903ebd76"] {
		t.Errorf("bodies = %v", bodies)
	}
}

func TestRecover_LogsOncePerBranch(t *testing.T) {
	h, logs := harnessWithLog(t)
	h.proc.setBranches([]branch{boomBranch{name_: "boom", where: "parse"}, boomBranch{name_: "bang", where: "claims"}})

	h.runBatch(batchWith("boom one", "boom two", "boom three"))
	h.runBatch(batchWith("boom four"))

	if n := logs.FilterField(zap.String("branch", "boom")).Len(); n != 0 {
		// the second branch is shadowed by the first for these lines; only boom runs
		if n != 1 {
			t.Errorf("boom logged %d times, want 1", n)
		}
	} else {
		t.Error("the panic of boom was not logged")
	}
	if logs.Len() != 1 {
		t.Errorf("%d log entries, want 1: %v", logs.Len(), logs.All())
	}
	if got := h.counter(parseFailedMetric, "format"); got["boom"] != 4 {
		t.Errorf("parse_failed = %v, want 4 for boom", got)
	}
}

func TestRecover_EachPanickingBranchLogsItsOwnEntry(t *testing.T) {
	h, logs := harnessWithLog(t)
	h.proc.setBranches([]branch{boomBranch{name_: "first", where: "parse"}})
	h.runBatch(batchWith("boom"))
	h.proc.setBranches([]branch{boomBranch{name_: "second", where: "claims"}})
	h.runBatch(batchWith("boom"))

	if logs.Len() != 2 {
		t.Errorf("%d log entries, want 2: %v", logs.Len(), logs.All())
	}
}

func TestRecover_TheLogEntryNamesTheBranchAndTheValue(t *testing.T) {
	h, logs := harnessWithLog(t)
	h.proc.setBranches([]branch{boomBranch{name_: "boom", where: "parse"}})

	h.runBatch(batchWith("boom"))

	if logs.Len() != 1 {
		t.Fatalf("%d log entries", logs.Len())
	}
	e := logs.All()[0]
	ctx := e.ContextMap()
	if ctx["branch"] != "boom" || !strings.Contains(e.Message+" "+ctx["panic"].(string), "parse blew up") {
		t.Errorf("entry = %q %v", e.Message, ctx)
	}
	if _, ok := ctx["stack"]; !ok {
		t.Errorf("no stack in the entry: %v", ctx)
	}
}
