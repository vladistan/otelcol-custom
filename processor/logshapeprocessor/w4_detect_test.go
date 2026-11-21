// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

// cspell:ignore mosquitto kaudit

package logshapeprocessor

import "testing"

// w4Fixtures holds one record of every format of this wave, keyed by the branch that must claim it.
var w4Fixtures = []struct {
	branch string
	in     input
}{
	{"rabbitmq", input{body: rabbitInfo}},
	{"mosquitto", input{body: "2026-11-19T22:56:53: New connection from 127.0.0.1:36468 on port 1883."}},
	{"stallcheck", input{body: "published StallMinutes=2"}},
	{"kaudit", input{body: auditAllowed}},
	{"smartd", input{body: "Device: /dev/ada3, 8 Offline uncorrectable sectors"}},
	{"gocd", input{body: gocdInfo}},
	{"cron", input{body: "cron: (root) CMD (/usr/local/bin/backup.sh)"}},
	{"openvpn", input{attrs: openvpnRecord()}},
	{"pbs", pbsInput(pbsGC, "2026-11-20T04:00:46+01:00: TASK OK")},
	{"pve", input{body: "INFO: done\nTASK OK", attrs: map[string]any{"log.file.path": pveDump}}},
	{"serialconsole", serialInput(serialOK)},
	{"logfmt", input{body: logfmtDashboard}},
	{"logfmt", input{body: logfmtInflux}},
	{"clickhouse", input{body: clickhouseInfo}},
	{"calico", input{body: calicoInfo}},
	{"ssmagent", input{body: ssmWarn}},
	{"litellm", input{body: litellmInfo}},
	{"lldpd", input{body: lldpdNeighbour}},
}

var w4Branches = []string{"rabbitmq", "mosquitto", "stallcheck", "kaudit", "smartd", "gocd", "cron", "openvpn", "pbs", "pve", "serialconsole", "logfmt", "clickhouse", "calico", "ssmagent", "litellm", "lldpd"}

func TestW4Detect_EveryFixture_IsClaimedByItsOwnBranchAndNoOther(t *testing.T) {
	for _, only := range w4Branches {
		for _, f := range w4Fixtures {
			cfg := defaultConfig()
			cfg.Branches = []string{only}
			h := newHarness(t, cfg)

			lr, _ := h.run(f.in)

			shaped := lr.Body().Str() != f.in.body || lr.Attributes().Len() != len(f.in.attrs) || lr.SeverityNumber() != 0
			if shaped != (only == f.branch) {
				t.Errorf("branch %q on a %q fixture (%.40q): shaped = %v", only, f.branch, f.in.body, shaped)
			}
		}
	}
}

func TestW4Detect_EveryFixture_IsClaimedWithAllBranchesOn(t *testing.T) {
	for _, f := range w4Fixtures {
		h := newHarness(t, defaultConfig())

		h.run(f.in)

		if got := h.counter(unmatchedMetric, "source"); len(got) != 0 {
			t.Errorf("%q fixture (%.40q) is unmatched: %v", f.branch, f.in.body, got)
		}
		if got := h.counter(parseFailedMetric, "format"); len(got) != 0 {
			t.Errorf("%q fixture (%.40q) failed to parse: %v", f.branch, f.in.body, got)
		}
	}
}

func TestW4Detect_KlogAndUvicornLines_AreLeftToTheirBranches(t *testing.T) {
	cfg := defaultConfig()
	cfg.Branches = w4Branches
	h := newHarness(t, cfg)

	for _, line := range []string{klogLine, uvicornLine} {
		lr, _ := h.run(input{body: line, ts: recordTime})
		if lr.Body().Str() != line || lr.Attributes().Len() != 0 {
			t.Errorf("a branch of this wave claimed %q", line)
		}
	}
}
