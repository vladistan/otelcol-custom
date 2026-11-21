// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

// cspell:ignore vstan

package logshapeprocessor

import "testing"

func TestInflux_HTTPDLine_DefaultConfigClaimsItForInflux(t *testing.T) {
	h := newHarness(t, defaultConfig())

	lr, _ := h.run(input{body: influxLine})

	if got := attrStr(t, lr.Attributes(), "vstan.influx.request_id"); got != "5a1b2c3d-1111-2222-3333-444455556666" {
		t.Errorf("request id = %q", got)
	}
	if got := h.counter(unmatchedMetric, "source"); len(got) != 0 {
		t.Errorf("unmatched = %v, want none", got)
	}
	if got := h.counter(parseFailedMetric, "format"); len(got) != 0 {
		t.Errorf("parse_failed = %v, want none", got)
	}
}
