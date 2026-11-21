// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

// cspell:ignore IWEF TDIWEC vstan awselb mosquitto kaudit

package logshapeprocessor

import (
	"slices"
	"strings"
)

// A gate lists what a branch needs before its claims can be true. A record that fails the gate is not
// offered to the branch, so most branches never run a regular expression on a line they cannot take.
// A gate may admit a record the branch then refuses; it must never refuse one the branch claims.

// bodyGate is one shape of body a branch can claim.
type bodyGate struct {
	first  string    // bytes the body may start with; empty allows any
	minLen int       // shortest claimed body
	at     []bytePos // bytes at fixed positions
	needs  []string  // substrings that must all occur
}

type bytePos struct {
	pos int
	b   byte
}

// gate is the union of the ways a branch claims: a body shape, a log.source value, or an attribute key
// on the record or the resource. A branch without a gate is always offered the record.
type gate struct {
	open   bool
	bodies []bodyGate
	srcs   []string
	keys   []string
}

func (g bodyGate) admits(body string) bool {
	if len(body) < g.minLen {
		return false
	}
	if g.first != "" && (body == "" || strings.IndexByte(g.first, body[0]) < 0) {
		return false
	}
	for _, p := range g.at {
		if p.pos >= len(body) || body[p.pos] != p.b {
			return false
		}
	}
	for _, s := range g.needs {
		if !strings.Contains(body, s) {
			return false
		}
	}
	return true
}

// emptyBody is the slot of the empty body in the table of first bytes.
const emptyBody = 256

// mayStart reports whether a body that starts with byte c, or is empty (c == emptyBody), can pass.
func (g bodyGate) mayStart(c int) bool {
	if c == emptyBody {
		return g.minLen == 0 && g.first == "" && len(g.at) == 0 && len(g.needs) == 0
	}
	return g.first == "" || strings.IndexByte(g.first, byte(c)) >= 0
}

// seen caches what the gates read from one record.
type seen struct {
	src     string
	haveSrc bool
}

// admits reports whether the record can be claimed by the branch the gate belongs to.
func (g *gate) admits(r *rec, s *seen) bool {
	if g.open {
		return true
	}
	for i := range g.bodies {
		if g.bodies[i].admits(r.body) {
			return true
		}
	}
	if len(g.srcs) > 0 {
		if !s.haveSrc {
			s.src, s.haveSrc = sourceOf(r.lr, r.res), true
		}
		if slices.Contains(g.srcs, s.src) {
			return true
		}
	}
	for _, k := range g.keys {
		if _, ok := r.lr.Attributes().Get(k); ok {
			return true
		}
		if _, ok := r.res.Attributes().Get(k); ok {
			return true
		}
	}
	return false
}

// entry is a branch with its hint and gate, found without a map lookup.
type entry struct {
	b    branch
	hint *Hint
	g    gate
}

// table holds the branches in claim order and, for each first byte of the body, the entries that may
// claim a body starting with it.
type table struct {
	entries []entry
	byFirst [emptyBody + 1][]int
}

func newTable(branches []branch, hints map[string]Hint, cfg *Config) *table {
	t := &table{entries: make([]entry, len(branches))}
	for i, b := range branches {
		e := entry{b: b, g: gateFor(b.name(), cfg)}
		if h, ok := hints[b.name()]; ok {
			e.hint = &h
		}
		t.entries[i] = e
	}
	for c := range t.byFirst {
		for i := range t.entries {
			if t.entries[i].g.mayStart(c) {
				t.byFirst[c] = append(t.byFirst[c], i)
			}
		}
	}
	return t
}

func (g *gate) mayStart(c int) bool {
	if g.open || len(g.srcs) > 0 || len(g.keys) > 0 {
		return true
	}
	for i := range g.bodies {
		if g.bodies[i].mayStart(c) {
			return true
		}
	}
	return false
}

// candidates are the entries that can claim a record with this body, in claim order.
func (t *table) candidates(body string) []int {
	if body == "" {
		return t.byFirst[emptyBody]
	}
	return t.byFirst[body[0]]
}

const (
	digits    = "0123456789"
	letters   = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	spaceLead = " \t\n\v\f\r\xc2\xe1\xe2\xe3" // an ASCII space, or the first byte of a Unicode one
	jsonFirst = "{" + spaceLead
)

// gateFor is the gate of the branch with this name. The conditions below are the necessary parts of each
// branch's claims; a branch not listed is always offered the record.
func gateFor(name string, cfg *Config) gate {
	body := func(b ...bodyGate) gate { return gate{bodies: b} }
	withKey := func(g gate, key string) gate {
		if key != "" {
			g.keys = append(g.keys, key)
		}
		return g
	}
	switch name {
	case "flow":
		return gate{keys: []string{"flow.src_as", "flow.dst_as"}}
	case "klog":
		return body(bodyGate{first: "IWEF", minLen: 20})
	case "uvicorn":
		return body(bodyGate{first: "TDIWEC", minLen: 5, needs: []string{":"}})
	case "collector":
		return gate{bodies: []bodyGate{{needs: []string{`"otelcol.component`}}}, keys: []string{"original_message"}}
	case "mongod":
		return body(bodyGate{first: jsonFirst, needs: []string{`"$date"`, `"msg"`}})
	case "gcpjson":
		return body(bodyGate{first: jsonFirst, needs: []string{`"severity"`, `"message"`}})
	case "tracing":
		return body(bodyGate{first: jsonFirst, needs: []string{`"fields"`, `"target"`, `"level"`}})
	case "pino":
		return body(bodyGate{needs: []string{"{", `"time"`, `"level"`}})
	case "logrus":
		return body(
			bodyGate{first: "{", minLen: 10},
			bodyGate{first: "t", minLen: 20, needs: []string{`time="`}},
			bodyGate{first: digits, minLen: 30, needs: []string{"\t"}},
		)
	case "postgres":
		g := body(bodyGate{minLen: 30, at: []bytePos{{4, '-'}, {7, '-'}, {10, ' '}}, needs: []string{"] "}})
		return withKey(g, "full_message")
	case "redis":
		return withKey(body(bodyGate{first: digits, minLen: 20, needs: []string{":"}}), cfg.Redis.SourceAttribute)
	case "nginx":
		return withKey(body(bodyGate{needs: []string{"["}}), cfg.Nginx.SourceAttribute)
	case "sentry":
		return withKey(body(bodyGate{minLen: 15, at: []bytePos{{2, ':'}, {5, ':'}}, needs: []string{"["}}), cfg.Sentry.SourceAttribute)
	case "cisco":
		g := gate{bodies: []bodyGate{{needs: []string{"%"}}, {first: "["}}, keys: []string{"vstan.cisco.facility", "cisco_facility"}}
		return withKey(g, cfg.Cisco.SourceAttribute)
	case "syslog":
		g := gate{bodies: []bodyGate{{first: digits + letters + spaceLead, minLen: 13}}, keys: []string{"priority"}}
		return withKey(g, cfg.Syslog.SourceAttribute)
	case "influx":
		return body(bodyGate{first: influxPrefix[:1], needs: []string{influxPrefix}})
	case "oauth2proxy":
		return body(bodyGate{needs: []string{" - ", " [", `"`}})
	case "coredns":
		return body(bodyGate{minLen: 20, needs: []string{" - ", `"`}})
	case "gcplb":
		return gate{srcs: []string{"gcp.lb"}}
	case "awselb":
		return gate{srcs: []string{"aws.elb"}}
	case "s3access":
		return gate{srcs: []string{"aws.s3access"}}
	case "gcpaudit":
		return gate{srcs: []string{"gcp.audit"}}
	case "eksaudit":
		return gate{srcs: []string{"aws.eks"}}
	case "cloudtrail":
		return gate{srcs: []string{"aws.cloudtrail"}}
	case "rabbitmq":
		return body(bodyGate{needs: []string{" ["}})
	case "mosquitto":
		return body(bodyGate{minLen: 21, at: []bytePos{{10, 'T'}, {19, ':'}}})
	case "stallcheck":
		return body(bodyGate{needs: []string{"published "}})
	case "kaudit":
		return body(bodyGate{first: "a", needs: []string{"audit: type=", "apparmor="}})
	case "smartd":
		return body(bodyGate{first: "D", needs: []string{"Device: "}})
	case "gocd":
		return body(bodyGate{first: "j", needs: []string{"jvm "}})
	case "cron":
		return body(bodyGate{first: "c", needs: []string{"cron: "}})
	case "openvpn":
		return gate{keys: []string{"common_name", "user.name"}}
	case "pbs", "pve":
		return gate{keys: []string{"log.file.name", "log.file.path"}}
	case "serialconsole":
		return gate{keys: []string{"cloud.resource_id"}}
	case "logfmt":
		return body(bodyGate{needs: []string{"msg="}})
	case "clickhouse":
		return body(bodyGate{first: digits, minLen: 30, needs: []string{"} <"}})
	case "calico":
		return body(bodyGate{first: digits, minLen: 30, at: []bytePos{{4, '-'}, {7, '-'}}, needs: []string{"]["}})
	case "ssmagent":
		return body(bodyGate{minLen: 25, at: []bytePos{{4, '-'}, {10, ' '}}})
	case "litellm":
		return body(bodyGate{needs: []string{" - "}})
	case "stderrhint":
		return gate{srcs: []string{"journal"}}
	case "lldpd":
		return body(bodyGate{first: "{", needs: []string{`"chassis_id"`, `"port_id"`, `"interface"`}})
	case "k8s":
		return gate{
			bodies: []bodyGate{
				{first: "P" + spaceLead, needs: []string{"Pod "}},
				{first: jsonFirst, needs: []string{`"involvedObject"`}},
				{first: jsonFirst, needs: []string{`"reason"`}},
			},
			keys: append([]string{"k8s.event.reason"}, k8sLiftKeys...),
		}
	}
	return gate{open: true}
}
