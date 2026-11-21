// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"fmt"
	"testing"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/processor"
	"go.opentelemetry.io/collector/processor/processortest"
)

// outRecord is one output record with the resource it travels under.
type outRecord struct {
	host, service, body string
	resource, attrs     map[string]any
	scope               string
	severity            plog.SeverityNumber
	severityText        string
}

func flatten(ld plog.Logs) []outRecord {
	var out []outRecord
	for i := range ld.ResourceLogs().Len() {
		rl := ld.ResourceLogs().At(i)
		res := rl.Resource().Attributes().AsRaw()
		for j := range rl.ScopeLogs().Len() {
			sl := rl.ScopeLogs().At(j)
			for k := range sl.LogRecords().Len() {
				lr := sl.LogRecords().At(k)
				f := outRecord{
					resource: res, attrs: lr.Attributes().AsRaw(), body: lr.Body().AsString(), scope: sl.Scope().Name(),
					severity: lr.SeverityNumber(), severityText: lr.SeverityText(),
				}
				f.host, _ = res["host.name"].(string)
				f.service, _ = res["service.name"].(string)
				out = append(out, f)
			}
		}
	}
	return out
}

// runBatch sends one batch and returns the batch the next consumer got.
func (h *harness) runBatch(ld plog.Logs) plog.Logs {
	h.t.Helper()
	if err := h.proc.ConsumeLogs(h.t.Context(), ld); err != nil {
		h.t.Fatalf("ConsumeLogs: %v", err)
	}
	all := h.sink.AllLogs()
	if len(all) == 0 {
		h.t.Fatal("nothing reached the next consumer")
	}
	return all[len(all)-1]
}

// line is one record of a test batch; msg goes to the "message" attribute and the body is "raw".
type line struct {
	msg  string
	peer string
	pri  string
}

// syslogBatch builds one ResourceLogs with one scope holding the lines in order.
func syslogBatch(resource map[string]any, lines ...line) plog.Logs {
	ld := plog.NewLogs()
	rl := ld.ResourceLogs().AppendEmpty()
	if resource != nil {
		_ = rl.Resource().Attributes().FromRaw(resource)
	}
	sl := rl.ScopeLogs().AppendEmpty()
	sl.Scope().SetName("test-scope")
	for _, l := range lines {
		lr := sl.LogRecords().AppendEmpty()
		lr.Attributes().PutStr("message", l.msg)
		lr.Body().SetStr("raw")
		if l.peer != "" {
			lr.Attributes().PutStr("net.peer.name", l.peer)
		}
		if l.pri != "" {
			lr.Attributes().PutStr("priority", l.pri)
		}
	}
	return ld
}

func newHarnessB(b *testing.B, cfg *Config) *shapeProcessor {
	b.Helper()
	proc, err := newProcessor(processortest.NewNopSettings(component.MustNewType(typeStr)), cfg, consumertest.NewNop())
	if err != nil {
		b.Fatal(err)
	}
	return proc
}

func newSettings() processor.Settings {
	return processortest.NewNopSettings(component.MustNewType(typeStr))
}

func nopConsumer() consumer.Logs { return consumertest.NewNop() }

func numberedLines(n int) []line {
	var lines []line
	for i := range n {
		lines = append(lines, line{msg: fmt.Sprintf("Dec 31 10:05:0%d alpha sshd[%d]: m%d", i, i, i)})
	}
	return lines
}
