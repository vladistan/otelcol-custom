// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package syslogprocessor

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/xpdata/entity"
)

// S1: the entity references of a resource travel with every group.
func TestConsume_EntityReferencesOfTheResourceReachEveryGroup(t *testing.T) {
	p, s := newTestProcessor()
	ld := syslogBatch(map[string]any{"cloud.region": "r1"},
		syslogLine{msg: "Dec 31 10:05:00 alpha sshd[1]: a"},
		syslogLine{msg: "Dec 31 10:05:01 beta cron[2]: b"},
	)
	ref := entity.ResourceEntityRefs(ld.ResourceLogs().At(0).Resource()).AppendEmpty()
	ref.SetType("cloud")
	ref.IdKeys().Append("cloud.region")
	ld.ResourceLogs().At(0).Resource().SetDroppedAttributesCount(3)

	consume(t, p, s, ld)
	out := s.got[0]
	if out.ResourceLogs().Len() != 2 {
		t.Fatalf("%d resources, want 2", out.ResourceLogs().Len())
	}
	for i := 0; i < out.ResourceLogs().Len(); i++ {
		res := out.ResourceLogs().At(i).Resource()
		refs := entity.ResourceEntityRefs(res)
		if refs.Len() != 1 || refs.At(0).Type() != "cloud" || refs.At(0).IdKeys().Len() != 1 {
			t.Errorf("resource %d lost its entity reference: %d refs", i, refs.Len())
		}
		if res.DroppedAttributesCount() != 3 {
			t.Errorf("resource %d dropped count = %d", i, res.DroppedAttributesCount())
		}
	}
}

// S2: the peer name is read from the record, and from the resource when the record has none.
func TestConsume_ThePeerNameComesFromTheRecordThenTheResource(t *testing.T) {
	p, s := newTestProcessor()
	got := consume(t, p, s, syslogBatch(map[string]any{"net.peer.name": "from-resource"},
		syslogLine{msg: "Dec 31 10:05:00 dhcp_server: one", peer: "from-record"},
		syslogLine{msg: "Dec 31 10:05:01 dhcp_server: two"},
	))
	byBody := map[string]outRecord{}
	for _, f := range got {
		byBody[f.body] = f
	}
	if f := byBody["one"]; f.host != "from-record" {
		t.Errorf("the record's peer name did not win: host = %q", f.host)
	}
	if f := byBody["two"]; f.host != "from-resource" {
		t.Errorf("the resource's peer name was not used: host = %q", f.host)
	}
}

// S3: a line with no HOSTNAME and no peer name leaves the host unset, whatever the batch resource says.
func TestConsume_ANoHostnameLineWithNoPeerNameClearsTheHostOfTheResource(t *testing.T) {
	p, s := newTestProcessor()
	got := consume(t, p, s, syslogBatch(map[string]any{"host.name": "relay"},
		syslogLine{msg: "Dec 31 10:05:00 dhcp_server: no peer"},
		syslogLine{msg: "Dec 31 10:05:01 dhcp_server: with peer", peer: "switch-a"},
		syslogLine{msg: "Dec 31 10:05:02 alpha sshd[1]: has a host"},
	))
	byBody := map[string]outRecord{}
	for _, f := range got {
		byBody[f.body] = f
	}
	if _, has := byBody["no peer"].resource["host.name"]; has {
		t.Errorf("host.name = %q, want it unset", byBody["no peer"].host)
	}
	if byBody["with peer"].host != "switch-a" || byBody["has a host"].host != "alpha" {
		t.Errorf("hosts = %q and %q", byBody["with peer"].host, byBody["has a host"].host)
	}
}

func TestConsume_WithSetHostnameOffTheHostOfTheResourceIsNotCleared(t *testing.T) {
	p, s := newTestProcessor(func(c *Config) { c.SetHostname = false })
	got := consume(t, p, s, syslogBatch(map[string]any{"host.name": "relay"}, syslogLine{msg: "Dec 31 10:05:00 dhcp_server: x"}))
	if got[0].host != "relay" {
		t.Errorf("host = %q, want relay", got[0].host)
	}
}

// S4: the colon rule keeps the content as it was and leaves IP addresses alone.
func TestParse_TheColonRuleKeepsContentAndHandlesEmptyBodiesAndAddresses(t *testing.T) {
	cases := []struct {
		line               string
		host, app, content string
		hostMissing        bool
	}{
		{"Dec 31 10:05:00 dhcp_server: a  b   c", "", "dhcp_server", "a  b   c", true},
		{"Dec 31 10:05:00 dhcp_server:", "", "dhcp_server", "", true},
		{"Dec 31 10:05:00   dhcp_server[7]:   spaced", "", "dhcp_server", "spaced", true},
		{"Dec 31 10:05:00 2001:db8:: sshd[1]: msg", "2001:db8::", "sshd", "msg", false},
		{"Dec 31 10:05:00 fe80::1 sshd[1]: msg", "fe80::1", "sshd", "msg", false},
		{"Dec 31 10:05:00 alpha sshd[1]: keep  spacing", "alpha", "sshd", "keep  spacing", false},
	}
	for _, c := range cases {
		info := Parse(c.line)
		if info == nil {
			t.Errorf("%q: no parse", c.line)
			continue
		}
		if info.Hostname != c.host || info.Appname != c.app || info.Content != c.content || info.HostMissing != c.hostMissing {
			t.Errorf("%q = host %q app %q content %q missing %v; want %q %q %q %v", c.line, info.Hostname, info.Appname, info.Content, info.HostMissing, c.host, c.app, c.content, c.hostMissing)
		}
	}
	info := Parse("Dec 31 10:05:00 2001:db8::1: text")
	if info == nil || info.Hostname != "2001:db8::1" || info.HostMissing || info.Content != "text" {
		t.Errorf("an address followed by a colon: %+v", info)
	}
}

// S5: the cost of a record does not grow with the width of its resource.
func wideBatch(attrs, records, hosts int) plog.Logs {
	ld := plog.NewLogs()
	rl := ld.ResourceLogs().AppendEmpty()
	for i := 0; i < attrs; i++ {
		rl.Resource().Attributes().PutStr(fmt.Sprintf("resource.attribute.%02d", i), strings.Repeat("v", 24))
	}
	sl := rl.ScopeLogs().AppendEmpty()
	for i := 0; i < records; i++ {
		lr := sl.LogRecords().AppendEmpty()
		lr.Attributes().PutStr("message", fmt.Sprintf("Dec 31 10:05:00 host%d sshd[%d]: message %d", i%hosts, i, i))
		lr.Attributes().PutStr("priority", "13")
		lr.Body().SetStr("raw")
	}
	return ld
}

func BenchmarkConsumeLogsWideResource(b *testing.B) {
	for _, c := range []struct {
		name         string
		attrs, hosts int
	}{{"narrow-4-hosts", 2, 4}, {"wide-4-hosts", 60, 4}, {"wide-1000-hosts", 60, 1000}} {
		b.Run(c.name, func(b *testing.B) {
			p, _ := newTestProcessor()
			b.ReportAllocs()
			for n := 0; n < b.N; n++ {
				b.StopTimer()
				ld := wideBatch(c.attrs, 1000, c.hosts)
				b.StartTimer()
				if err := p.ConsumeLogs(context.Background(), ld); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func TestConsume_AWideResourceGivesTheSameGroupsAsANarrowOne(t *testing.T) {
	p, s := newTestProcessor()
	consume(t, p, s, wideBatch(60, 40, 4))
	out := s.got[0]
	if out.ResourceLogs().Len() != 4 {
		t.Fatalf("%d resources, want 4", out.ResourceLogs().Len())
	}
	for i := 0; i < out.ResourceLogs().Len(); i++ {
		rl := out.ResourceLogs().At(i)
		if rl.Resource().Attributes().Len() != 62 {
			t.Errorf("resource %d has %d attributes, want 60 plus host.name and service.name", i, rl.Resource().Attributes().Len())
		}
	}
}

// S6, documented: two input ResourceLogs with equal resources stay two groups; a ResourceLogs with no
// records produces nothing.
func TestConsume_GroupsAreMadeInsideOneInputResourceLogsAndEmptyOnesAreDropped(t *testing.T) {
	p, s := newTestProcessor()
	ld := plog.NewLogs()
	for i := 0; i < 2; i++ {
		rl := ld.ResourceLogs().AppendEmpty()
		rl.Resource().Attributes().PutStr("cloud.region", "r1")
		lr := rl.ScopeLogs().AppendEmpty().LogRecords().AppendEmpty()
		lr.Attributes().PutStr("message", "Dec 31 10:05:00 alpha sshd[1]: x")
	}
	ld.ResourceLogs().AppendEmpty().Resource().Attributes().PutStr("cloud.region", "empty")
	consume(t, p, s, ld)
	if n := s.got[0].ResourceLogs().Len(); n != 2 {
		t.Errorf("%d resources, want 2: one for each input ResourceLogs that has records", n)
	}
}
