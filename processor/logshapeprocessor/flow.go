// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"github.com/vladistan/otelcol-custom/shape/rename"
)

// flowRules are the only names this branch changes. The other flow.* keys, among them flow.type, which
// drives retention, stay as the receiver wrote them.
var flowRules = []rename.Rule{
	{From: "flow.src_as", To: "source.as.number"},
	{From: "flow.dst_as", To: "destination.as.number"},
}

type flowBranch struct{}

func newFlowBranch() *flowBranch { return &flowBranch{} }

func (*flowBranch) name() string { return "flow" }

// claims a flow record that still carries one of the two AS number keys of the netflow receiver.
func (*flowBranch) claims(r *rec) bool {
	a := r.lr.Attributes()
	if _, ok := a.Get("flow.src_as"); ok {
		return true
	}
	_, ok := a.Get("flow.dst_as")
	return ok
}

func (*flowBranch) parse(r *rec) error {
	rename.ApplyRules(r.lr.Attributes(), flowRules)
	return nil
}
