// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package k8seventprocessor

import (
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
)

// parityFields holds what the kube-v0 transform/k8s_objects block sets on one record.
// It is built before the other handlers run, because they may replace the body it reads.
type parityFields struct {
	attrs   pcommon.Map
	message string
	sevNum  plog.SeverityNumber
	sevText string
}

// collectParity reads wrapper (the map holding "type", when the format has one) and object.
func collectParity(wrapper pcommon.Map, hasWrapper bool, object pcommon.Map, kind string) parityFields {
	f := parityFields{attrs: pcommon.NewMap()}
	a := f.attrs

	switch kind {
	case "Pod":
		f.sevNum, f.sevText = plog.SeverityNumberInfo, "INFO"
	case "Event":
		switch getNestedString(object, "type") {
		case "Warning":
			f.sevNum, f.sevText = plog.SeverityNumberWarn, "WARN"
		case "Normal":
			f.sevNum, f.sevText = plog.SeverityNumberInfo, "INFO"
		}
	}

	a.PutStr("k8s.object.kind", kind)
	var action pcommon.Value
	hasAction := false
	if hasWrapper {
		action, hasAction = lookup(wrapper, "type")
	}
	if hasAction {
		putCopy(a, "k8s.object.action", action)
	}
	namespace, hasNamespace := lookup(object, "metadata", "namespace")
	name, hasName := lookup(object, "metadata", "name")
	putIf(a, "k8s.namespace", namespace, hasNamespace)
	putIf(a, "k8s.name", name, hasName)

	var detail string
	switch kind {
	case "Event":
		detail = eventParity(a, object)
	case "Pod":
		detail = podParity(a, object)
	}

	putLabel(a, object, "k8s.labels.app", "app")
	putLabel(a, object, "k8s.labels.environment", "environment")
	putLabel(a, object, "k8s.labels.job_name", "job-name")

	f.message = "K8S"
	if hasAction {
		f.message += " " + action.AsString()
	}
	f.message += " " + kind
	if hasNamespace && hasName {
		f.message += " " + namespace.AsString() + " / " + name.AsString()
	}
	f.message += detail

	a.PutStr("message_type", "k8sobject")
	a.PutStr("message", f.message)
	object.CopyTo(a.PutEmptyMap("k8s.object.full"))
	return f
}

// apply writes the staged fields over whatever the other handlers set.
func (f parityFields) apply(lr plog.LogRecord) {
	f.attrs.Range(func(k string, v pcommon.Value) bool {
		v.CopyTo(lr.Attributes().PutEmpty(k))
		return true
	})
	if f.sevText != "" {
		lr.SetSeverityNumber(f.sevNum)
		lr.SetSeverityText(f.sevText)
	}
	lr.Body().SetStr(f.message)
}

// eventParity sets the Event-only fields and returns the message suffix.
func eventParity(a pcommon.Map, object pcommon.Map) string {
	for _, f := range []struct{ attr, key string }{
		{"k8s.event.type", "type"},
		{"k8s.event.reason", "reason"},
		{"k8s.event.note", "note"},
		{"k8s.event.count", "deprecatedCount"},
	} {
		v, ok := lookup(object, f.key)
		putIf(a, f.attr, v, ok)
	}
	for _, f := range []struct{ attr, key string }{
		{"k8s.event.regarding.kind", "kind"},
		{"k8s.event.regarding.name", "name"},
		{"k8s.event.regarding.namespace", "namespace"},
	} {
		v, ok := lookup(object, "regarding", f.key)
		putIf(a, f.attr, v, ok)
	}
	if reason, ok := lookup(object, "reason"); ok {
		return " " + reason.AsString()
	}
	return ""
}

// podParity sets the Pod-only fields and returns the message suffix.
func podParity(a pcommon.Map, object pcommon.Map) string {
	phase, hasPhase := lookup(object, "status", "phase")
	putIf(a, "k8s.pod.phase", phase, hasPhase)

	if statuses, ok := lookup(object, "status", "containerStatuses"); ok &&
		statuses.Type() == pcommon.ValueTypeSlice && statuses.Slice().Len() > 0 &&
		statuses.Slice().At(0).Type() == pcommon.ValueTypeMap {
		state := statuses.Slice().At(0).Map()
		if terminated, ok := lookup(state, "state", "terminated"); ok {
			if code, ok := lookup(terminatedMap(terminated), "exitCode"); ok {
				putCopy(a, "k8s.container.exit_code", code)
			}
			a.PutStr("k8s.container.state", "terminated")
		}
		if _, ok := lookup(state, "state", "running"); ok {
			a.PutStr("k8s.container.state", "running")
		}
	}

	if hasPhase {
		return " Phase:" + phase.AsString()
	}
	return ""
}

func terminatedMap(v pcommon.Value) pcommon.Map {
	if v.Type() == pcommon.ValueTypeMap {
		return v.Map()
	}
	return pcommon.NewMap()
}

func putLabel(a pcommon.Map, object pcommon.Map, attr, label string) {
	v, ok := lookup(object, "metadata", "labels", label)
	putIf(a, attr, v, ok)
}

func putIf(a pcommon.Map, key string, v pcommon.Value, ok bool) {
	if ok {
		putCopy(a, key, v)
	}
}

func putCopy(a pcommon.Map, key string, v pcommon.Value) {
	v.CopyTo(a.PutEmpty(key))
}

// lookup follows keys through nested maps; a missing key, a non-map step or an empty value reports false.
func lookup(m pcommon.Map, keys ...string) (pcommon.Value, bool) {
	for i, key := range keys {
		v, ok := m.Get(key)
		if !ok || v.Type() == pcommon.ValueTypeEmpty {
			return pcommon.Value{}, false
		}
		if i == len(keys)-1 {
			return v, true
		}
		if v.Type() != pcommon.ValueTypeMap {
			return pcommon.Value{}, false
		}
		m = v.Map()
	}
	return pcommon.Value{}, false
}
