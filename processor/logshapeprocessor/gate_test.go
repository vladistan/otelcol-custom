// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"slices"
	"sync"
	"testing"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
)

// gateConfig names a source attribute for every branch that has one, so the gates of those branches
// are tested with the attribute route open.
func gateConfig() *Config {
	cfg := defaultConfig()
	cfg.Nginx.SourceAttribute = "line"
	cfg.Redis.SourceAttribute = "line"
	cfg.Sentry.SourceAttribute = "line"
	cfg.Cisco.SourceAttribute = "line"
	cfg.Syslog.SourceAttribute = "line"
	return cfg
}

func newRec(lr plog.LogRecord, res pcommon.Resource, body string) *rec {
	return &rec{lr: lr, res: res, scope: pcommon.NewInstrumentationScope(), body: body}
}

// gateRecords are the shapes a gate must admit when the branch claims: the text in the body and in the
// attributes a branch reads. withSources adds each log.source a branch keys on, on the record and on
// the resource; the body does not matter for those, so only a few texts need them.
func gateRecords(text string, withSources bool) []*rec {
	var out []*rec
	for _, lr := range recordsFor(text) {
		out = append(out, newRec(lr, pcommon.NewResource(), bodyText(lr)))
	}
	withKeys := plog.NewLogRecord()
	withKeys.Body().SetStr(text)
	for _, k := range []string{"line", "log.file.name", "log.file.path", "cloud.resource_id", "common_name", "user.name",
		"bytes_received", "bytes_sent", "vstan.openvpn.bytes_received", "vstan.openvpn.bytes_sent", "k8s.event.reason", "k8s.pod.name"} {
		withKeys.Attributes().PutStr(k, text)
	}
	out = append(out, newRec(withKeys, pcommon.NewResource(), text))
	if !withSources {
		return out
	}
	for _, src := range []string{"lb", "elb", "s3access", "audit", "eks", "cloudtrail", "journal"} {
		onRecord := plog.NewLogRecord()
		onRecord.Body().SetStr(text)
		onRecord.Attributes().PutStr("log.source", src)
		out = append(out, newRec(onRecord, pcommon.NewResource(), text))

		onResource := plog.NewLogRecord()
		onResource.Body().SetStr(text)
		res := pcommon.NewResource()
		res.Attributes().PutStr("log.source", src)
		out = append(out, newRec(onResource, res, text))
	}
	return out
}

// gateInputs are the odd inputs, and every sample line with each kind of leading space and with each
// first byte a branch looks for, so a gate that lists too few first bytes is caught. The second result
// holds the sample-based texts, which also get the log.source records.
func gateInputs(tb testing.TB) (odd, samples []string) {
	tb.Helper()
	odd = oddInputs(tb)
	for _, lines := range loadSamples(tb) {
		for _, line := range lines {
			samples = append(samples, line)
			for _, lead := range []string{" ", "\t", "\n", " ", "\u0085", " ", "　"} {
				samples = append(samples, lead+line)
			}
			for _, c := range "IWEFTDCPjcdat{[0" {
				samples = append(samples, string(c)+line[1:])
			}
		}
	}
	return odd, samples
}

// sweep is the result of offering every gate input to every branch and to the table.
type sweep struct {
	refused    []string // a gate refused a record its branch claims
	diverged   []string // the table's first claim differs from the first claim of a linear scan
	claims     map[string]int
	branchName []string
}

var (
	sweepOnce   sync.Once
	sweepResult sweep
)

// runSweep does the work once for the tests below: it takes seconds under the race detector.
func runSweep(t *testing.T) *sweep {
	t.Helper()
	sweepOnce.Do(func() {
		cfg := gateConfig()
		branches, err := newBranches(cfg)
		if err != nil {
			t.Fatal(err)
		}
		table := newTable(branches, nil, cfg)
		sweepResult.claims = map[string]int{}
		for _, b := range branches {
			sweepResult.branchName = append(sweepResult.branchName, b.name())
		}
		offer := func(r *rec) {
			linear := ""
			for i, b := range branches {
				if ok, _ := safeClaims(b, r); !ok {
					continue
				}
				sweepResult.claims[b.name()]++
				if linear == "" {
					linear = b.name()
				}
				if !table.entries[i].g.admits(r, &seen{}) {
					sweepResult.refused = append(sweepResult.refused, b.name()+": "+shorten(r.body))
				}
			}
			indexed := ""
			var s seen
			for _, i := range table.candidates(r.body) {
				e := &table.entries[i]
				if ok, _ := safeClaims(e.b, r); e.g.admits(r, &s) && ok {
					indexed = e.b.name()
					break
				}
			}
			if indexed != linear {
				sweepResult.diverged = append(sweepResult.diverged, shorten(r.body)+": table "+indexed+", linear "+linear)
			}
		}
		odd, samples := gateInputs(t)
		for _, text := range odd {
			for _, r := range gateRecords(text, false) {
				offer(r)
			}
		}
		for _, text := range samples {
			for _, r := range gateRecords(text, true) {
				offer(r)
			}
		}
	})
	return &sweepResult
}

func TestGate_NoGateRefusesARecordItsBranchClaims(t *testing.T) {
	for _, miss := range runSweep(t).refused {
		t.Errorf("gate refuses a claimed record: %s", miss)
	}
}

func TestGate_TheFirstClaimThroughTheTableIsTheFirstClaimOfALinearScan(t *testing.T) {
	for _, miss := range runSweep(t).diverged {
		t.Errorf("claims differ: %s", miss)
	}
}

func TestGate_TheSweepMakesEveryTextBranchClaimSomething(t *testing.T) {
	res := runSweep(t)
	for _, name := range res.branchName {
		if res.claims[name] == 0 && !attributeKeyed[name] && !journalKeyed[name] && !slices.Contains(sourceKeyedBranches, name) {
			t.Errorf("no input made branch %s claim: its gate is untested", name)
		}
	}
}

func TestGate_APlainLineIsOfferedToFewBranches(t *testing.T) {
	cfg := defaultConfig()
	branches, err := newBranches(cfg)
	if err != nil {
		t.Fatal(err)
	}
	tbl := newTable(branches, nil, cfg)
	r := newRec(plog.NewLogRecord(), pcommon.NewResource(), "plain line that no parser claims")

	var tried []string
	var s seen
	for _, i := range tbl.candidates(r.body) {
		if tbl.entries[i].g.admits(r, &s) {
			tried = append(tried, tbl.entries[i].b.name())
		}
	}

	for _, name := range []string{"klog", "uvicorn", "postgres", "redis", "nginx", "sentry", "cisco", "logfmt", "clickhouse", "calico", "mosquitto"} {
		if slices.Contains(tried, name) {
			t.Errorf("branch %s is offered a plain line", name)
		}
	}
	if len(tried) > len(branches)/4 {
		t.Errorf("%d of %d branches are offered a plain line: %v", len(tried), len(branches), tried)
	}
}

func TestGate_ABranchWithoutAGateIsAlwaysOffered(t *testing.T) {
	g := gateFor("a-branch-with-no-gate", defaultConfig())
	if !g.admits(newRec(plog.NewLogRecord(), pcommon.NewResource(), ""), &seen{}) {
		t.Fatal("an unknown branch must see every record")
	}
}

func TestGate_SourceAttributeLinesReachTheirBranchWithAnEmptyBody(t *testing.T) {
	samples := loadSamples(t)
	for _, name := range []string{"nginx", "redis", "sentry"} {
		t.Run(name, func(t *testing.T) {
			cfg := defaultConfig()
			cfg.Branches = []string{name}
			switch name {
			case "nginx":
				cfg.Nginx.SourceAttribute = "line"
			case "redis":
				cfg.Redis.SourceAttribute = "line"
			case "sentry":
				cfg.Sentry.SourceAttribute = "line"
			}
			h := newHarness(t, cfg)
			ld := plog.NewLogs()
			lr := ld.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty().LogRecords().AppendEmpty()
			lr.Attributes().PutStr("line", samples[name][0])

			h.runBatch(ld)

			if got := h.counter(unmatchedMetric, "source"); len(got) != 0 {
				t.Errorf("unmatched = %v: the gate hid the source attribute", got)
			}
		})
	}
}
