// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import "testing"

// A line that passes the cheap check of a branch but not its parse is no line of the branch: claims
// says no, and the record counts as unmatched, not as a failed parse.
func TestLookalike_ALineThatPassesTheQuickCheckButNotTheParseIsUnmatched(t *testing.T) {
	for name, line := range map[string]string{
		"klog":   "I1119 23:05:39.184859 this has no pid and no file:line",
		"sentry": "02:30:21 [WARNING] ?",
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, defaultConfig())

			lr, _ := h.run(input{body: line})

			if lr.Body().Str() != line {
				t.Errorf("body changed to %q", lr.Body().Str())
			}
			if got := h.counter(unmatchedMetric, "source"); got["none"] != 1 {
				t.Errorf("unmatched = %v, want one record", got)
			}
			if got := h.counter(parseFailedMetric, "format"); len(got) != 0 {
				t.Errorf("parse_failed = %v, want none", got)
			}
		})
	}
}
