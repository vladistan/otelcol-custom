// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"reflect"
	"testing"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
)

type auditRec struct {
	body  string
	attrs map[string]any
}

type auditShaped struct {
	lr  plog.LogRecord
	res pcommon.Resource
}

// batchLogs puts every record under one shared resource and scope.
func auditBatchLogs(resource map[string]any, recs []auditRec) plog.Logs {
	ld := plog.NewLogs()
	rl := ld.ResourceLogs().AppendEmpty()
	if err := rl.Resource().Attributes().FromRaw(resource); err != nil {
		panic(err)
	}
	lrs := rl.ScopeLogs().AppendEmpty().LogRecords()
	for _, r := range recs {
		lr := lrs.AppendEmpty()
		lr.Body().SetStr(r.body)
		if err := lr.Attributes().FromRaw(r.attrs); err != nil {
			panic(err)
		}
	}
	return ld
}

// flatten lists the records of a payload in output order with their own resource.
func auditFlatten(ld plog.Logs) []auditShaped {
	var out []auditShaped
	for i := range ld.ResourceLogs().Len() {
		rl := ld.ResourceLogs().At(i)
		for j := range rl.ScopeLogs().Len() {
			lrs := rl.ScopeLogs().At(j).LogRecords()
			for k := range lrs.Len() {
				out = append(out, auditShaped{lr: lrs.At(k), res: rl.Resource()})
			}
		}
	}
	return out
}

func (h *harness) auditBatch(resource map[string]any, recs []auditRec) []auditShaped {
	h.t.Helper()
	if err := h.proc.ConsumeLogs(h.t.Context(), auditBatchLogs(resource, recs)); err != nil {
		h.t.Fatalf("ConsumeLogs: %v", err)
	}
	all := h.sink.AllLogs()
	return auditFlatten(all[len(all)-1])
}

// assertTwoPassesEqual feeds the shaped output of a first pass to a second
// processor and checks that nothing changes.
func assertTwoPassesEqual(t *testing.T, cfg *Config, resource map[string]any, recs []auditRec) {
	t.Helper()
	first := newHarness(t, cfg)
	once := first.auditBatch(resource, recs)
	second := newHarness(t, cfg)
	// The processor moves records out of its input, so feed it a copy.
	payload := plog.NewLogs()
	first.sink.AllLogs()[0].CopyTo(payload)
	if err := second.proc.ConsumeLogs(t.Context(), payload); err != nil {
		t.Fatal(err)
	}
	twice := auditFlatten(second.sink.AllLogs()[0])
	if len(once) != len(twice) {
		t.Fatalf("records after one pass %d, after two %d", len(once), len(twice))
	}
	for i := range once {
		a, b := once[i], twice[i]
		if a.lr.Body().Str() != b.lr.Body().Str() || a.lr.SeverityNumber() != b.lr.SeverityNumber() ||
			a.lr.SeverityText() != b.lr.SeverityText() ||
			!reflect.DeepEqual(a.lr.Attributes().AsRaw(), b.lr.Attributes().AsRaw()) ||
			!reflect.DeepEqual(a.res.Attributes().AsRaw(), b.res.Attributes().AsRaw()) {
			t.Errorf("record %d changed on the second pass:\n first  %q %v %v\n second %q %v %v", i,
				a.lr.Body().Str(), a.lr.Attributes().AsRaw(), a.res.Attributes().AsRaw(),
				b.lr.Body().Str(), b.lr.Attributes().AsRaw(), b.res.Attributes().AsRaw())
		}
	}
}
