// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"errors"
	"strconv"
	"strings"
)

const (
	autoscalerCountKey  = "autoscaledNodesCount"
	autoscalerTargetKey = "autoscaledNodesTarget"
	autoscalerNodesAttr = "vstan.autoscaler.nodes"
	autoscalerGoalAttr  = "vstan.autoscaler.target"
)

// autoscalerStatus holds the two decoded values claims found in the status object.
type autoscalerStatus struct{ count, target any }

// autoscalerBranch shapes the status rows of the cluster autoscaler, a JSON object whose status holds the
// node count and the target. Rows with a count that differs from the target are kept; the processor
// samples the rows where they are equal.
type autoscalerBranch struct{}

func newAutoscalerBranch() *autoscalerBranch { return &autoscalerBranch{} }

func (*autoscalerBranch) name() string { return "autoscaler" }

func (*autoscalerBranch) claims(r *rec) bool {
	st, ok := readAutoscalerStatus(r.body)
	if ok {
		r.memo = st
	}
	return ok
}

func (*autoscalerBranch) parse(r *rec) error {
	st := memoOf[autoscalerStatus](r)
	if st == nil {
		var ok bool
		if st, ok = readAutoscalerStatus(r.body); !ok {
			return errors.New("not an autoscaler status row")
		}
	}
	nodes, okNodes := st.count.(int64)
	target, okTarget := st.target.(int64)
	if !okNodes || !okTarget {
		return errors.New("autoscaler node count or target is not an integer")
	}
	a := r.lr.Attributes()
	a.PutStr(originalKey, r.body)
	a.PutInt(autoscalerNodesAttr, nodes)
	a.PutInt(autoscalerGoalAttr, target)
	r.lr.Body().SetStr("autoscaler nodes " + strconv.FormatInt(nodes, 10) + "/" + strconv.FormatInt(target, 10))
	return nil
}

// readAutoscalerStatus returns the count and target of a JSON body whose status object has both keys, of any type.
func readAutoscalerStatus(body string) (*autoscalerStatus, bool) {
	if !strings.HasPrefix(body, "{") || !strings.Contains(body, `"`+autoscalerCountKey+`"`) ||
		!strings.Contains(body, `"`+autoscalerTargetKey+`"`) {
		return nil, false
	}
	o, err := parseJSONObject(body)
	if err != nil {
		return nil, false
	}
	status, ok := o.object("status")
	if !ok {
		return nil, false
	}
	count, hasCount := status[autoscalerCountKey]
	target, hasTarget := status[autoscalerTargetKey]
	if !hasCount || !hasTarget {
		return nil, false
	}
	return &autoscalerStatus{count: count, target: target}, true
}

// sampledOut reports whether a shaped autoscaler row is dropped: only a row whose count equals its target
// is sampled, and it is kept when the draw falls below the keep ratio.
func (p *shapeProcessor) sampledOut(r *rec) bool {
	a := r.lr.Attributes()
	nodes, okNodes := a.Get(autoscalerNodesAttr)
	target, okTarget := a.Get(autoscalerGoalAttr)
	if !okNodes || !okTarget || nodes.Int() != target.Int() {
		return false
	}
	return p.draw() >= p.cfg.Autoscaler.KeepRatio
}
