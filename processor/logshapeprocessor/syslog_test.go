// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"fmt"
	"reflect"
	"testing"

	"go.opentelemetry.io/collector/pdata/plog"
)

func TestSyslog_EachRecordKeepsTheHostAndServiceOfItsOwnLine(t *testing.T) {
	h := newHarness(t, defaultConfig())
	ld := h.runBatch(syslogBatch(nil,
		line{msg: "Dec 31 10:05:00 alpha sshd[1]: one"},
		line{msg: "Dec 31 10:05:01 beta cron[2]: two"},
		line{msg: "Dec 31 10:05:02 alpha sshd[3]: three"},
	))
	got := flatten(ld)
	want := []struct{ host, service, body string }{
		{"alpha", "sshd", "one"}, {"alpha", "sshd", "three"}, {"beta", "cron", "two"},
	}
	if len(got) != len(want) {
		t.Fatalf("%d records, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].host != w.host || got[i].service != w.service || got[i].body != w.body {
			t.Errorf("record %d = %s/%s/%s, want %s/%s/%s", i, got[i].host, got[i].service, got[i].body, w.host, w.service, w.body)
		}
	}
	if n := ld.ResourceLogs().Len(); n != 2 {
		t.Errorf("%d resources, want 2: one for each host and service", n)
	}
}

func TestSyslog_ASingleHostBatchStaysOneResourceInOrder(t *testing.T) {
	h := newHarness(t, defaultConfig())
	ld := h.runBatch(syslogBatch(nil, numberedLines(5)...))
	if n := ld.ResourceLogs().Len(); n != 1 {
		t.Fatalf("%d resources, want 1", n)
	}
	for i, f := range flatten(ld) {
		if f.body != fmt.Sprintf("m%d", i) || f.host != "alpha" || f.service != "sshd" {
			t.Errorf("record %d = %+v", i, f)
		}
	}
}

func TestSyslog_TheSameHostWithTwoServicesSplits(t *testing.T) {
	h := newHarness(t, defaultConfig())
	ld := h.runBatch(syslogBatch(nil,
		line{msg: "Dec 31 10:05:00 alpha sshd[1]: a"},
		line{msg: "Dec 31 10:05:01 alpha cron[2]: b"},
	))
	got := flatten(ld)
	if got[0].service != "sshd" || got[1].service != "cron" || got[0].host != "alpha" || got[1].host != "alpha" {
		t.Errorf("got %+v", got)
	}
	if n := ld.ResourceLogs().Len(); n != 2 {
		t.Errorf("%d resources, want 2", n)
	}
}

func TestSyslog_TheOtherResourceAttributesAndTheScopeTravelWithEveryGroup(t *testing.T) {
	h := newHarness(t, defaultConfig())
	ld := h.runBatch(syslogBatch(map[string]any{"cloud.region": "r1", "deployment.environment.name": "lab"},
		line{msg: "Dec 31 10:05:00 alpha sshd[1]: a"},
		line{msg: "Dec 31 10:05:01 beta cron[2]: b"},
	))
	for i, f := range flatten(ld) {
		if f.resource["cloud.region"] != "r1" || f.resource["deployment.environment.name"] != "lab" {
			t.Errorf("record %d lost a resource attribute: %v", i, f.resource)
		}
		if f.scope != "test-scope" {
			t.Errorf("record %d scope = %q", i, f.scope)
		}
	}
}

func TestSyslog_ALineThatDoesNotParseKeepsTheHostOfTheResource(t *testing.T) {
	h := newHarness(t, defaultConfig())
	ld := h.runBatch(syslogBatch(map[string]any{"host.name": "relay", "service.name": "udp"},
		line{msg: "not a syslog line"},
		line{msg: "Dec 31 10:05:01 beta cron[2]: b"},
	))
	byBody := map[string]outRecord{}
	for _, f := range flatten(ld) {
		byBody[f.body] = f
	}
	if f := byBody["raw"]; f.host != "relay" || f.service != "udp" || f.attrs["log.source"] != "syslog" {
		t.Errorf("unparsed record = %s/%s %v, want relay/udp tagged syslog", f.host, f.service, f.attrs)
	}
	if f := byBody["b"]; f.host != "beta" || f.service != "cron" {
		t.Errorf("parsed record = %s/%s, want beta/cron", f.host, f.service)
	}
}

func TestSyslog_AColonTokenIsTheTagAndTheHostComesFromThePeerName(t *testing.T) {
	h := newHarness(t, defaultConfig())
	ld := h.runBatch(syslogBatch(nil,
		line{msg: "Dec 31 10:05:00 dhcp_server: lease granted", peer: "switch-a"},
		line{msg: "Dec 31 10:05:01 dhcp_server[7]: lease renewed", peer: "switch-b"},
		line{msg: "Dec 31 10:05:02 dhcp_server: no peer"},
	))
	byBody := map[string]outRecord{}
	for _, f := range flatten(ld) {
		byBody[f.body] = f
	}
	if f := byBody["lease granted"]; f.host != "switch-a" || f.service != "dhcp_server" {
		t.Errorf("first = %s/%s", f.host, f.service)
	}
	if f := byBody["lease renewed"]; f.host != "switch-b" || f.service != "dhcp_server" || f.attrs["process.pid"] != int64(7) {
		t.Errorf("second = %s/%s pid %v", f.host, f.service, f.attrs["process.pid"])
	}
	f := byBody["no peer"]
	if _, has := f.resource["host.name"]; has {
		t.Errorf("a line with no hostname and no peer name got host.name %q", f.host)
	}
	if f.service != "dhcp_server" {
		t.Errorf("service = %q, want dhcp_server", f.service)
	}
}

func TestSyslog_WithSetHostnameOffTheHostDoesNotSplit(t *testing.T) {
	cfg := defaultConfig()
	cfg.Syslog.SetHostname = false
	h := newHarness(t, cfg)
	ld := h.runBatch(syslogBatch(map[string]any{"host.name": "relay"},
		line{msg: "Dec 31 10:05:00 alpha sshd[1]: a"},
		line{msg: "Dec 31 10:05:01 beta sshd[2]: b"},
	))
	for i, f := range flatten(ld) {
		if f.host != "relay" || f.service != "sshd" {
			t.Errorf("record %d = %s/%s", i, f.host, f.service)
		}
	}
	if n := ld.ResourceLogs().Len(); n != 1 {
		t.Errorf("%d resources, want 1", n)
	}
}

func TestSyslog_TheOtherAttributesAndTheCountStayAsTheyWere(t *testing.T) {
	h := newHarness(t, defaultConfig())
	got := flatten(h.runBatch(syslogBatch(nil, line{msg: "Dec 31 10:05:00 alpha sshd[42]: hello", pri: "13"})))
	if len(got) != 1 {
		t.Fatalf("%d records", len(got))
	}
	want := map[string]any{
		"message": "Dec 31 10:05:00 alpha sshd[42]: hello", "priority": "13",
		"log.record.original": "Dec 31 10:05:00 alpha sshd[42]: hello", "process.pid": int64(42), "log.source": "syslog",
	}
	if !reflect.DeepEqual(got[0].attrs, want) {
		t.Errorf("attributes = %v\nwant %v", got[0].attrs, want)
	}
	if got[0].body != "hello" {
		t.Errorf("body = %q", got[0].body)
	}
	if got[0].severity != plog.SeverityNumberInfo || got[0].severityText != "INFO" {
		t.Errorf("severity = %d %q", got[0].severity, got[0].severityText)
	}
}

func TestSyslog_NoPriority_LeavesTheSeverityUnset(t *testing.T) {
	h := newHarness(t, defaultConfig())
	got := flatten(h.runBatch(syslogBatch(nil, line{msg: "Dec 31 10:05:00 alpha sshd[42]: hello"})))
	if got[0].severity != plog.SeverityNumberUnspecified || got[0].severityText != "" {
		t.Errorf("severity = %d %q, want unset", got[0].severity, got[0].severityText)
	}
}

func TestSyslog_DisabledBranch_LeavesTheRecordAlone(t *testing.T) {
	cfg := defaultConfig()
	cfg.Branches = []string{"klog"}
	h := newHarness(t, cfg)
	got := flatten(h.runBatch(syslogBatch(nil, line{msg: "Dec 31 10:05:00 alpha sshd[42]: hello", pri: "13"})))
	if got[0].body != "raw" || got[0].attrs["log.source"] != nil {
		t.Errorf("record changed: %+v", got[0])
	}
}

func BenchmarkSyslogConsume(b *testing.B) {
	hosts := []string{"alpha", "beta", "gamma", "delta"}
	h := newHarnessB(b, defaultConfig())
	b.ReportAllocs()
	for b.Loop() {
		ld := plog.NewLogs()
		sl := ld.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty()
		for i := range 1000 {
			lr := sl.LogRecords().AppendEmpty()
			lr.Attributes().PutStr("message", fmt.Sprintf("Dec 31 10:05:00 %s sshd[%d]: message %d", hosts[i%len(hosts)], i, i))
			lr.Attributes().PutStr("priority", "13")
			lr.Body().SetStr("raw")
		}
		if err := h.ConsumeLogs(b.Context(), ld); err != nil {
			b.Fatal(err)
		}
	}
}
