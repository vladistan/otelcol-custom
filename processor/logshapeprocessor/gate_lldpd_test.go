// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

// cspell:ignore lldpd

package logshapeprocessor

import (
	"slices"
	"testing"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
)

func TestGate_LldpdAdmitsItsNeighbourRecordsAndRefusesOtherText(t *testing.T) {
	g := gateFor("lldpd", gateConfig())
	if g.open {
		t.Fatal("the lldpd gate is open")
	}
	bodies := []string{
		lldpdNeighbour,
		`{"interface":"eno2","chassis_id":"00:11:22:33:44:55","port_id":"Gi4/0/18"}`,
	}
	bodies = append(bodies, loadSamples(t)["lldpd"]...)
	for _, body := range bodies {
		r := newRec(plog.NewLogRecord(), pcommon.NewResource(), body)
		if !g.admits(r, &seen{}) {
			t.Errorf("gate refuses an lldpd record: %s", shorten(body))
		}
	}
	for _, body := range []string{"", "Nov 20 13:06:13 host sshd[1]: session opened", `{"msg":"hello"}`, `{"chassis_id":"a","port_id":"b"}`} {
		r := newRec(plog.NewLogRecord(), pcommon.NewResource(), body)
		if g.admits(r, &seen{}) {
			t.Errorf("gate admits text that is not lldpd: %q", body)
		}
	}
}

// openGateAllowed names the branches that cannot be gated, each with the reason. It is empty.
var openGateAllowed = map[string]string{}

func TestGate_EveryBranchHasAGateUnlessAllowed(t *testing.T) {
	cfg := gateConfig()
	branches, err := newBranches(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range branches {
		_, allowed := openGateAllowed[b.name()]
		if gateFor(b.name(), cfg).open && !allowed {
			t.Errorf("branch %s has no gate", b.name())
		}
		if !gateFor(b.name(), cfg).open && allowed {
			t.Errorf("branch %s is allowed an open gate but has one", b.name())
		}
	}
	for name := range openGateAllowed {
		if !slices.ContainsFunc(branches, func(b branch) bool { return b.name() == name }) {
			t.Errorf("allow list names an unknown branch %s", name)
		}
	}
}
