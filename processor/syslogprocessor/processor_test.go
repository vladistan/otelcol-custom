// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package syslogprocessor

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.uber.org/zap"
)

type captureSink struct{ got []plog.Logs }

func (s *captureSink) Capabilities() consumer.Capabilities { return consumer.Capabilities{} }

func (s *captureSink) ConsumeLogs(_ context.Context, ld plog.Logs) error {
	s.got = append(s.got, ld)
	return nil
}

func newTestProcessor(mutate ...func(*Config)) (*syslogProcessor, *captureSink) {
	cfg := createDefaultConfig().(*Config)
	for _, m := range mutate {
		m(cfg)
	}
	s := &captureSink{}
	return newProcessor(zap.NewNop(), cfg, s), s
}

// syslogLine is one syslog message with the peer name the receiver would have set.
type syslogLine struct {
	msg  string
	peer string
	pri  string
}

// syslogBatch builds one ResourceLogs with one scope holding the lines in order.
func syslogBatch(resource map[string]any, lines ...syslogLine) plog.Logs {
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
		lr.Body().SetStr(l.msg)
		if l.peer != "" {
			lr.Attributes().PutStr("net.peer.name", l.peer)
		}
		if l.pri != "" {
			lr.Attributes().PutStr("priority", l.pri)
		}
	}
	return ld
}

// outRecord is one output record with the resource it travels under.
type outRecord struct {
	host, service, body string
	resource, attrs     map[string]any
	scope               string
}

func flattenLogs(ld plog.Logs) []outRecord {
	var out []outRecord
	for i := 0; i < ld.ResourceLogs().Len(); i++ {
		rl := ld.ResourceLogs().At(i)
		res := rl.Resource().Attributes().AsRaw()
		for j := 0; j < rl.ScopeLogs().Len(); j++ {
			sl := rl.ScopeLogs().At(j)
			for k := 0; k < sl.LogRecords().Len(); k++ {
				lr := sl.LogRecords().At(k)
				f := outRecord{resource: res, attrs: lr.Attributes().AsRaw(), body: lr.Body().AsString(), scope: sl.Scope().Name()}
				f.host, _ = res["host.name"].(string)
				f.service, _ = res["service.name"].(string)
				out = append(out, f)
			}
		}
	}
	return out
}

func consume(t *testing.T, p *syslogProcessor, s *captureSink, ld plog.Logs) []outRecord {
	t.Helper()
	if err := p.ConsumeLogs(context.Background(), ld); err != nil {
		t.Fatal(err)
	}
	if len(s.got) != 1 {
		t.Fatalf("the next consumer got %d batches, want 1", len(s.got))
	}
	return flattenLogs(s.got[0])
}

func TestConsume_EachRecordKeepsTheHostAndServiceOfItsOwnLine(t *testing.T) {
	p, s := newTestProcessor()
	got := consume(t, p, s, syslogBatch(nil,
		syslogLine{msg: "Dec 31 10:05:00 alpha sshd[1]: one"},
		syslogLine{msg: "Dec 31 10:05:01 beta cron[2]: two"},
		syslogLine{msg: "Dec 31 10:05:02 alpha sshd[3]: three"},
	))
	if len(got) != 3 {
		t.Fatalf("%d records, want 3", len(got))
	}
	want := []struct{ host, service, body string }{
		{"alpha", "sshd", "one"}, {"alpha", "sshd", "three"}, {"beta", "cron", "two"},
	}
	for i, w := range want {
		if got[i].host != w.host || got[i].service != w.service || got[i].body != w.body {
			t.Errorf("record %d = %s/%s/%s, want %s/%s/%s", i, got[i].host, got[i].service, got[i].body, w.host, w.service, w.body)
		}
	}
	if n := s.got[0].ResourceLogs().Len(); n != 2 {
		t.Errorf("%d resources, want 2: one for each host and service", n)
	}
}

func TestConsume_ASingleHostBatchStaysOneResourceInOrder(t *testing.T) {
	p, s := newTestProcessor()
	var lines []syslogLine
	for i := 0; i < 5; i++ {
		lines = append(lines, syslogLine{msg: fmt.Sprintf("Dec 31 10:05:0%d alpha sshd[%d]: m%d", i, i, i)})
	}
	got := consume(t, p, s, syslogBatch(nil, lines...))
	if n := s.got[0].ResourceLogs().Len(); n != 1 {
		t.Fatalf("%d resources, want 1", n)
	}
	for i, f := range got {
		if f.body != fmt.Sprintf("m%d", i) || f.host != "alpha" || f.service != "sshd" {
			t.Errorf("record %d = %+v", i, f)
		}
	}
}

func TestConsume_TheSameHostWithTwoServicesSplits(t *testing.T) {
	p, s := newTestProcessor()
	got := consume(t, p, s, syslogBatch(nil,
		syslogLine{msg: "Dec 31 10:05:00 alpha sshd[1]: a"},
		syslogLine{msg: "Dec 31 10:05:01 alpha cron[2]: b"},
	))
	if got[0].service != "sshd" || got[1].service != "cron" || got[0].host != "alpha" || got[1].host != "alpha" {
		t.Errorf("got %+v", got)
	}
	if n := s.got[0].ResourceLogs().Len(); n != 2 {
		t.Errorf("%d resources, want 2", n)
	}
}

func TestConsume_TheOtherResourceAttributesAndTheScopeTravelWithEveryGroup(t *testing.T) {
	p, s := newTestProcessor()
	got := consume(t, p, s, syslogBatch(map[string]any{"cloud.region": "r1", "deployment.environment": "lab"},
		syslogLine{msg: "Dec 31 10:05:00 alpha sshd[1]: a"},
		syslogLine{msg: "Dec 31 10:05:01 beta cron[2]: b"},
	))
	for i, f := range got {
		if f.resource["cloud.region"] != "r1" || f.resource["deployment.environment"] != "lab" {
			t.Errorf("record %d lost a resource attribute: %v", i, f.resource)
		}
		if f.scope != "test-scope" {
			t.Errorf("record %d scope = %q", i, f.scope)
		}
	}
}

func TestConsume_ALineThatDoesNotParseKeepsTheHostOfTheResource(t *testing.T) {
	p, s := newTestProcessor()
	got := consume(t, p, s, syslogBatch(map[string]any{"host.name": "relay", "service.name": "udp"},
		syslogLine{msg: "not a syslog line"},
		syslogLine{msg: "Dec 31 10:05:01 beta cron[2]: b"},
	))
	byBody := map[string]outRecord{}
	for _, f := range got {
		byBody[f.body] = f
	}
	if f := byBody["not a syslog line"]; f.host != "relay" || f.service != "udp" {
		t.Errorf("unparsed record = %s/%s, want relay/udp", f.host, f.service)
	}
	if f := byBody["b"]; f.host != "beta" || f.service != "cron" {
		t.Errorf("parsed record = %s/%s, want beta/cron", f.host, f.service)
	}
}

func TestConsume_AColonTokenIsTheTagAndTheHostComesFromThePeerName(t *testing.T) {
	p, s := newTestProcessor()
	got := consume(t, p, s, syslogBatch(nil,
		syslogLine{msg: "Dec 31 10:05:00 dhcp_server: lease granted", peer: "switch-a"},
		syslogLine{msg: "Dec 31 10:05:01 dhcp_server[7]: lease renewed", peer: "switch-b"},
		syslogLine{msg: "Dec 31 10:05:02 dhcp_server: no peer"},
	))
	byBody := map[string]outRecord{}
	for _, f := range got {
		byBody[f.body] = f
	}
	if f := byBody["lease granted"]; f.host != "switch-a" || f.service != "dhcp_server" {
		t.Errorf("first = %s/%s", f.host, f.service)
	}
	if f := byBody["lease renewed"]; f.host != "switch-b" || f.service != "dhcp_server" || f.attrs["process.pid"] != "7" {
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

func TestConsume_WithSetHostnameOffTheHostDoesNotSplit(t *testing.T) {
	p, s := newTestProcessor(func(c *Config) { c.SetHostname = false })
	got := consume(t, p, s, syslogBatch(map[string]any{"host.name": "relay"},
		syslogLine{msg: "Dec 31 10:05:00 alpha sshd[1]: a"},
		syslogLine{msg: "Dec 31 10:05:01 beta sshd[2]: b"},
	))
	for i, f := range got {
		if f.host != "relay" || f.service != "sshd" {
			t.Errorf("record %d = %s/%s", i, f.host, f.service)
		}
	}
	if n := s.got[0].ResourceLogs().Len(); n != 1 {
		t.Errorf("%d resources, want 1", n)
	}
}

func TestConsume_TheOtherAttributesAndTheCountStayAsTheyWere(t *testing.T) {
	p, s := newTestProcessor()
	got := consume(t, p, s, syslogBatch(nil, syslogLine{msg: "Dec 31 10:05:00 alpha sshd[42]: hello", pri: "13"}))
	if len(got) != 1 {
		t.Fatalf("%d records", len(got))
	}
	want := map[string]any{
		"message": "Dec 31 10:05:00 alpha sshd[42]: hello", "priority": "13",
		"original_message": "Dec 31 10:05:00 alpha sshd[42]: hello", "process.pid": "42", "log.source": "syslog",
	}
	if !reflect.DeepEqual(got[0].attrs, want) {
		t.Errorf("attributes = %v\nwant %v", got[0].attrs, want)
	}
	if got[0].body != "hello" {
		t.Errorf("body = %q", got[0].body)
	}
	lr := s.got[0].ResourceLogs().At(0).ScopeLogs().At(0).LogRecords().At(0)
	if lr.SeverityText() != "INFO" {
		t.Errorf("severity = %q", lr.SeverityText())
	}
}

func TestParse_AColonTokenIsTheTagNotTheHost(t *testing.T) {
	info := Parse("Dec 31 10:05:00 dhcp_server: lease granted")
	if info == nil {
		t.Fatal("no parse")
	}
	if info.Hostname != "" || info.Appname != "dhcp_server" || info.Content != "lease granted" || !info.HostMissing {
		t.Errorf("info = %+v", info)
	}
	info = Parse("Dec 31 10:05:00 alpha sshd[1]: ok")
	if info.Hostname != "alpha" || info.HostMissing {
		t.Errorf("info = %+v", info)
	}
}

func BenchmarkConsumeLogs(b *testing.B) {
	hosts := []string{"alpha", "beta", "gamma", "delta"}
	p, _ := newTestProcessor()
	b.ReportAllocs()
	for n := 0; n < b.N; n++ {
		ld := plog.NewLogs()
		sl := ld.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty()
		for i := 0; i < 1000; i++ {
			lr := sl.LogRecords().AppendEmpty()
			lr.Attributes().PutStr("message", fmt.Sprintf("Dec 31 10:05:00 %s sshd[%d]: message %d", hosts[i%len(hosts)], i, i))
			lr.Attributes().PutStr("priority", "13")
			lr.Body().SetStr("raw")
		}
		if err := p.ConsumeLogs(context.Background(), ld); err != nil {
			b.Fatal(err)
		}
	}
}
