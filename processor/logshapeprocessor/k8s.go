// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"encoding/json"
	"errors"
	"strings"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"

	"github.com/vladistan/otelcol-custom/shape/k8slift"
	"github.com/vladistan/otelcol-custom/shape/severity"
)

// k8sLiftKeys are the record attributes that make the branch lift names into the resource.
var k8sLiftKeys = []string{"k8s.namespace.name", "k8s.pod.name", "k8s.node.name", "k8s.cluster.name"}

// k8sMaxDecodeBytes bounds the body that is JSON-decoded looking for an event: real events
// are a few KiB and a decode costs about 85 ms per MiB.
const k8sMaxDecodeBytes = 256 << 10

// k8sBranch shapes Kubernetes pod status lines, object events and records that carry
// Kubernetes names in the record attributes.
type k8sBranch struct {
	staticPrefixes []string
}

func newK8sBranch(cfg K8sConfig) *k8sBranch {
	return &k8sBranch{staticPrefixes: cfg.StaticPodNodes}
}

func (*k8sBranch) name() string { return "k8s" }

func (*k8sBranch) claims(r *rec) bool {
	if _, ok := k8slift.ParsePodStatus(r.body); ok {
		return true
	}
	if _, ok := k8sObjectEvent(r.body); ok {
		return true
	}
	a := r.lr.Attributes()
	if k8sStr(a, "k8s.event.reason") != "" {
		return true
	}
	for _, k := range k8sLiftKeys {
		if k8sStr(a, k) != "" {
			return true
		}
	}
	return false
}

func (b *k8sBranch) parse(r *rec) error {
	if ps, ok := k8slift.ParsePodStatus(r.body); ok {
		b.podStatus(r, ps)
		return nil
	}
	if e, ok := k8sObjectEvent(r.body); ok {
		b.event(r, e)
		return nil
	}
	a := r.lr.Attributes()
	if reason := k8sStr(a, "k8s.event.reason"); reason != "" {
		b.event(r, k8sEventFromAttrs(a, r.body, reason))
		return nil
	}
	for _, k := range k8sLiftKeys {
		if k8sStr(a, k) != "" {
			b.lift(r)
			return nil
		}
	}
	return errors.New("not a Kubernetes record")
}

// podStatus takes names from the line itself: the record attributes may carry only part of them.
func (b *k8sBranch) podStatus(r *rec, ps k8slift.PodStatus) {
	a, ra := r.lr.Attributes(), r.res.Attributes()
	k8slift.Lift(ra, a)
	ra.PutStr("k8s.namespace.name", ps.Namespace)
	ra.PutStr("k8s.pod.name", ps.Pod)
	ra.PutStr("service.name", ps.Pod)
	node := ps.Node
	if node == "" {
		node, _ = k8slift.NodeFromStaticPod(ps.Pod, b.staticPrefixes)
	}
	if node != "" {
		ra.PutStr("k8s.node.name", node)
		ra.PutStr("host.name", node)
	}
	for _, k := range []string{"k8s.namespace.name", "k8s.pod.name", "k8s.node.name", "k8s.pod.phase"} {
		a.Remove(k)
	}
	a.PutStr("k8s.pod.status.phase", ps.Phase)
	k8sSetSeverity(r.lr, severity.Level{})
	r.lr.Body().SetStr(ps.Text())
}

func (b *k8sBranch) event(r *rec, e k8slift.ObjectEvent) {
	a, ra := r.lr.Attributes(), r.res.Attributes()
	k8slift.Lift(ra, a)
	e.Message = k8sNote(e.Message)
	if text := e.Text(); text != "" {
		r.lr.Body().SetStr(text)
	}
	if svc := e.Service(); svc != "" {
		ra.PutStr("service.name", svc)
	}
	if e.Host != "" {
		ra.PutStr("host.name", e.Host)
	}
	k8sPutMissing(ra, "k8s.namespace.name", e.Namespace)
	switch e.Kind {
	case "Pod":
		k8sPutMissing(ra, "k8s.pod.name", e.Name)
	case "Node":
		k8sPutMissing(ra, "k8s.node.name", e.Name)
		k8sPutMissing(ra, "host.name", e.Name)
	}
	var level severity.Level
	switch e.Type {
	case "Normal":
		level, _ = severity.FromSource(severity.Word, "info")
	case "Warning":
		level, _ = severity.FromSource(severity.Word, "warn")
	}
	k8sSetSeverity(r.lr, level)
}

func (b *k8sBranch) lift(r *rec) {
	ra := r.res.Attributes()
	k8slift.Lift(ra, r.lr.Attributes())
	if _, has := ra.Get("k8s.node.name"); has {
		return
	}
	pod, ok := ra.Get("k8s.pod.name")
	if !ok {
		return
	}
	if node, ok := k8slift.NodeFromStaticPod(pod.Str(), b.staticPrefixes); ok {
		ra.PutStr("k8s.node.name", node)
		k8sPutMissing(ra, "host.name", node)
	}
}

// k8sObjectEvent decodes a body only when it can be an event: it starts with "{", is not
// huge and holds one of the keys an event needs, so other JSON never pays for a decode.
func k8sObjectEvent(body string) (k8slift.ObjectEvent, bool) {
	if len(body) > k8sMaxDecodeBytes || !strings.HasPrefix(strings.TrimSpace(body), "{") {
		return k8slift.ObjectEvent{}, false
	}
	if !k8sHasEventKeys(body) {
		return k8slift.ObjectEvent{}, false
	}
	return k8slift.ParseObjectEvent(body)
}

// k8sHasEventKeys is the cheap pre-check before a JSON decode.
func k8sHasEventKeys(body string) bool {
	return strings.Contains(body, `"involvedObject"`) || strings.Contains(body, `"reason"`)
}

// k8sEventFromAttrs reads an event the receiver already split into attributes. When the
// note is not an attribute it is the text after the reason in an earlier rewritten body.
func k8sEventFromAttrs(a pcommon.Map, body, reason string) k8slift.ObjectEvent {
	e := k8slift.ObjectEvent{
		Kind:      k8sStr(a, "k8s.event.regarding.kind"),
		Name:      k8sStr(a, "k8s.event.regarding.name"),
		Namespace: k8sStr(a, "k8s.event.regarding.namespace"),
		Reason:    reason,
		Message:   k8sStr(a, "k8s.event.note"),
	}
	// The type is the Event type only on Event objects; other rows carry a watch action there.
	if kind := k8sStr(a, "k8s.object.kind"); kind == "" || kind == "Event" {
		e.Type = k8sStr(a, "k8s.event.type")
	}
	if e.Message == "" {
		if _, note, ok := strings.Cut(body, ": "+reason+" - "); ok {
			e.Message = note
		}
	}
	return e
}

// k8sNote turns a JSON note with a code and a message into "message (code)".
func k8sNote(msg string) string {
	t := strings.TrimSpace(msg)
	if !strings.HasPrefix(t, "{") {
		return msg
	}
	var n struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal([]byte(t), &n); err != nil || strings.TrimSpace(n.Message) == "" {
		return msg
	}
	text := strings.TrimRight(strings.TrimSpace(n.Message), ".")
	if n.Code == "" {
		return text
	}
	return text + " (" + n.Code + ")"
}

func k8sSetSeverity(lr plog.LogRecord, l severity.Level) {
	lr.SetSeverityNumber(plog.SeverityNumber(l.Number))
	lr.SetSeverityText(l.Text)
}

func k8sStr(m pcommon.Map, key string) string {
	if v, ok := m.Get(key); ok && v.Type() == pcommon.ValueTypeStr {
		return v.Str()
	}
	return ""
}

func k8sPutMissing(m pcommon.Map, key, value string) {
	if value == "" {
		return
	}
	if _, has := m.Get(key); !has {
		m.PutStr(key, value)
	}
}
