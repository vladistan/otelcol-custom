// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"testing"

	"go.opentelemetry.io/collector/pdata/plog"
)

func flowInput(attrs map[string]any) input {
	all := map[string]any{"flow.type": "netflow_v9", "flow.sampler_address": "192.0.2.1", "source.address": "10.0.0.5"}
	for k, v := range attrs {
		all[k] = v
	}
	return input{attrs: all}
}

func TestFlow_AsNumbers_AreRenamedAndStayIntegers(t *testing.T) {
	h := newHarness(t, defaultConfig())

	lr, _ := h.run(flowInput(map[string]any{"flow.src_as": int64(65000), "flow.dst_as": int64(65001)}))

	a := lr.Attributes()
	attrInt(t, a, "source.as.number", 65000)
	attrInt(t, a, "destination.as.number", 65001)
	absent(t, a, "flow.src_as", "flow.dst_as")
}

func TestFlow_TheKeysThatDriveRetention_AreKept(t *testing.T) {
	h := newHarness(t, defaultConfig())

	lr, _ := h.run(flowInput(map[string]any{"flow.src_as": int64(1), "flow.dst_as": int64(2)}))

	a := lr.Attributes()
	if attrStr(t, a, "flow.type") != "netflow_v9" || attrStr(t, a, "flow.sampler_address") != "192.0.2.1" || attrStr(t, a, "source.address") != "10.0.0.5" {
		t.Errorf("attributes = %v", a.AsRaw())
	}
}

func TestFlow_AsNumberAsText_BecomesAnInteger(t *testing.T) {
	h := newHarness(t, defaultConfig())

	lr, _ := h.run(flowInput(map[string]any{"flow.src_as": "13335"}))

	attrInt(t, lr.Attributes(), "source.as.number", 13335)
}

func TestFlow_OneAsNumberIsEnough(t *testing.T) {
	h := newHarness(t, defaultConfig())

	lr, _ := h.run(flowInput(map[string]any{"flow.dst_as": int64(15169)}))

	attrInt(t, lr.Attributes(), "destination.as.number", 15169)
	absent(t, lr.Attributes(), "source.as.number")
}

func TestFlow_ZeroAsNumbers_AreWritten(t *testing.T) {
	h := newHarness(t, defaultConfig())

	lr, _ := h.run(flowInput(map[string]any{"flow.src_as": int64(0), "flow.dst_as": int64(0)}))

	attrInt(t, lr.Attributes(), "source.as.number", 0)
	attrInt(t, lr.Attributes(), "destination.as.number", 0)
}

func TestFlow_BodyAndSeverity_AreLeftAlone(t *testing.T) {
	h := newHarness(t, defaultConfig())

	lr, _ := h.run(flowInput(map[string]any{"flow.src_as": int64(1)}))

	if lr.Body().Str() != "" || lr.SeverityNumber() != plog.SeverityNumberUnspecified {
		t.Errorf("body %q severity %d", lr.Body().Str(), lr.SeverityNumber())
	}
}

func TestFlow_ARecordWithoutAsNumbers_IsNotClaimed(t *testing.T) {
	h := newHarness(t, defaultConfig())

	lr, _ := h.run(flowInput(nil))

	absent(t, lr.Attributes(), "source.as.number", "destination.as.number")
	if got := h.counter(unmatchedMetric, "source"); got["none"] != 1 {
		t.Errorf("unmatched = %v", got)
	}
}

func TestFlow_DisabledBranch_LeavesTheNames(t *testing.T) {
	cfg := defaultConfig()
	cfg.Branches = []string{"klog"}
	h := newHarness(t, cfg)

	lr, _ := h.run(flowInput(map[string]any{"flow.src_as": int64(65000)}))

	attrInt(t, lr.Attributes(), "flow.src_as", 65000)
}
