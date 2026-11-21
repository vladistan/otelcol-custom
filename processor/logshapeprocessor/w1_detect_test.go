// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"maps"
	"reflect"
	"testing"
)

// detectFixtures holds one record of every format of the table, keyed by the branch that owns it.
var detectFixtures = []struct {
	branch string
	also   string // a second branch that claims the fixture; the order of branchNames puts branch first
	in     input
}{
	{"collector", "logrus", input{body: collectorStandard}},
	{"collector", "", input{body: collectorEdge}},
	{"collector", "", input{body: collectorWrapped}},
	{"collector", "", input{body: nodeLogsTail, attrs: map[string]any{"original_message": collectorDebug, "otelcol.component": "debug"}}},
	{"tracing", "logrus", input{body: tracingInfo}},
	{"gcpjson", "logrus", input{body: gcpInfo}},
	{"pino", "", input{body: pinoError}},
	{"mongod", "", input{body: mongodEnded}},
	{"mongod", "", input{body: mongodAuth}},
	{"klog", "", input{body: klogLine, ts: recordTime}},
	{"uvicorn", "", input{body: uvicornLine}},
	{"logrus", "", input{body: `time="2025-12-31T04:25:29Z" level=warning msg="slow sync" duration=3s`}},
	{"postgres", "", input{body: "2026-11-19 22:42:20.918 UTC [1] ERROR:  something happened"}},
	{"redis", "", input{body: "1:M 19 Nov 2026 23:22:37.151 * ready"}},
	{"nginx", "", input{body: nginxLine, ts: recordTime}},
	{"sentry", "", input{body: "02:30:21 [WARNING] sentry.tasks: Retrying the task", ts: sentryRecordTime}},
	{"cisco", "", input{body: "%LINK-3-UPDOWN: Interface GigabitEthernet0/1, changed state to down"}},
	{"syslog", "", input{body: "Dec 31 10:05:00 alpha sshd[1]: one"}},
}

func shapedBy(t *testing.T, branches []string, in input) bool {
	t.Helper()
	cfg := defaultConfig()
	cfg.Branches = branches
	lr, _ := newHarness(t, cfg).run(in)
	return lr.Body().Str() != in.body || lr.Attributes().Len() != len(in.attrs) || lr.SeverityNumber() != 0
}

func TestDetect_EveryFixture_IsClaimedByItsOwnBranchAndOnlyKnownOthers(t *testing.T) {
	for _, only := range branchNames {
		for _, f := range detectFixtures {
			want := only == f.branch || only == f.also
			if got := shapedBy(t, []string{only}, f.in); got != want {
				t.Errorf("branch %q on a %q fixture (%.40q): shaped = %v, want %v", only, f.branch, f.in.body, got, want)
			}
		}
	}
}

func TestDetect_OwnBranchWinsWithAllBranchesOn(t *testing.T) {
	for _, f := range detectFixtures {
		alone := onlyHarness(t, f.branch)
		all := newHarness(t, defaultConfig())

		want, _ := alone.run(f.in)
		got, _ := all.run(f.in)

		if got.Body().Str() != want.Body().Str() || got.SeverityNumber() != want.SeverityNumber() || !reflect.DeepEqual(got.Attributes().AsRaw(), want.Attributes().AsRaw()) {
			t.Errorf("%q fixture (%.40q): the table gives %q %v, the branch alone gives %q %v",
				f.branch, f.in.body, got.Body().Str(), got.Attributes().AsRaw(), want.Body().Str(), want.Attributes().AsRaw())
		}
		if failed := all.counter(parseFailedMetric, "format"); len(failed) != 0 {
			t.Errorf("%q fixture failed to parse: %v", f.branch, failed)
		}
		if unmatched := all.counter(unmatchedMetric, "source"); len(unmatched) != 0 {
			t.Errorf("%q fixture is unmatched: %v", f.branch, unmatched)
		}
	}
}

func TestDetect_JsonThatNoBranchKnows_PassesUnchangedAndIsCounted(t *testing.T) {
	for _, body := range []string{`{"foo":"bar"}`, `{"level":"info"}`, `{not json`, `[1,2,3]`, `{"msg":"only a message"}`} {
		h := newHarness(t, defaultConfig())

		lr, _ := h.run(input{body: body})

		if lr.Body().Str() != body || lr.Attributes().Len() != 0 {
			t.Errorf("%q changed: %q %v", body, lr.Body().Str(), lr.Attributes().AsRaw())
		}
		if got := h.counter(unmatchedMetric, "source"); !maps.Equal(got, map[string]int64{"none": 1}) {
			t.Errorf("%q: unmatched = %v", body, got)
		}
	}
}
