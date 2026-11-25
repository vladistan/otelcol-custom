// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

// Package k8slift moves Kubernetes names from a record into its resource and reads pod status and object event lines.
package k8slift

import (
	"encoding/json"
	"regexp"
	"strings"

	"go.opentelemetry.io/collector/pdata/pcommon"
)

type liftKey struct {
	target  string
	sources []string
}

var liftKeys = []liftKey{
	{"k8s.namespace.name", []string{"k8s.namespace.name", "namespace"}},
	{"k8s.pod.name", []string{"k8s.pod.name", "pod"}},
	{"k8s.node.name", []string{"k8s.node.name", "node"}},
	{"k8s.cluster.name", []string{"k8s.cluster.name", "cluster"}},
}

// Lift moves the namespace, pod, node and cluster from the record attributes into the resource
// attributes, then sets host.name from the node when the resource has none. A value the resource already holds wins.
func Lift(res, rec pcommon.Map) {
	for _, k := range liftKeys {
		if _, has := res.Get(k.target); has {
			continue
		}
		for _, src := range k.sources {
			v, ok := rec.Get(src)
			if !ok || v.Str() == "" {
				continue
			}
			res.PutStr(k.target, v.Str())
			rec.Remove(src)
			break
		}
	}
	if _, has := res.Get("host.name"); !has {
		if node, ok := res.Get("k8s.node.name"); ok && node.Str() != "" {
			res.PutStr("host.name", node.Str())
		}
	}
}

// PodStatus is a parsed pod status line.
type PodStatus struct {
	Namespace string
	Pod       string
	Phase     string
	Ready     string
	Node      string
}

var podStatusLine = regexp.MustCompile(`^Pod ([^/\s]+)/(\S+): (\w+)(?: \((\d+/\d+) ready\))?(?: on (\S+))?$`)

// ParsePodStatus reads "Pod ns/name: Phase (r/n ready) on node".
func ParsePodStatus(line string) (PodStatus, bool) {
	m := podStatusLine.FindStringSubmatch(strings.TrimSpace(line))
	if m == nil {
		return PodStatus{}, false
	}
	return PodStatus{Namespace: m[1], Pod: m[2], Phase: m[3], Ready: m[4], Node: m[5]}, true
}

// Text is the body of a pod status record: "Phase (r/n ready)".
func (s PodStatus) Text() string {
	if s.Ready == "" {
		return s.Phase
	}
	return s.Phase + " (" + s.Ready + " ready)"
}

var staticPodPrefixes = []string{"kube-apiserver-", "kube-controller-manager-", "kube-scheduler-", "etcd-"}

// NodeFromStaticPod returns the node a static pod runs on, read from its name. Extra prefixes extend the defaults.
func NodeFromStaticPod(pod string, extra []string) (string, bool) {
	for _, p := range append(append([]string{}, staticPodPrefixes...), extra...) {
		if node, ok := strings.CutPrefix(pod, p); ok && node != "" {
			return node, true
		}
	}
	return "", false
}

// ObjectEvent is a Kubernetes event object read from JSON.
type ObjectEvent struct {
	Kind      string
	Name      string
	Namespace string
	Reason    string
	Message   string
	Type      string
	Host      string
}

type eventJSON struct {
	InvolvedObject struct {
		Kind      string `json:"kind"`
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
	} `json:"involvedObject"`
	Reason  string `json:"reason"`
	Message string `json:"message"`
	Note    string `json:"note"`
	Type    string `json:"type"`
	Source  struct {
		Host string `json:"host"`
	} `json:"source"`
}

// ParseObjectEvent reads the JSON of a Kubernetes event. It needs a reason or an involved object.
func ParseObjectEvent(doc string) (ObjectEvent, bool) {
	doc = strings.TrimSpace(doc)
	if !strings.HasPrefix(doc, "{") {
		return ObjectEvent{}, false
	}
	var e eventJSON
	if err := json.Unmarshal([]byte(doc), &e); err != nil {
		return ObjectEvent{}, false
	}
	if e.Reason == "" && e.InvolvedObject.Kind == "" {
		return ObjectEvent{}, false
	}
	message := e.Note // the note of the newer event form wins over the message of the old one
	if message == "" {
		message = e.Message
	}
	return ObjectEvent{
		Kind: e.InvolvedObject.Kind, Name: e.InvolvedObject.Name, Namespace: e.InvolvedObject.Namespace,
		Reason: e.Reason, Message: message, Type: e.Type, Host: e.Source.Host,
	}, true
}

// Text is the body of an event record: "Reason: message".
func (e ObjectEvent) Text() string {
	switch {
	case e.Reason != "" && e.Message != "":
		return e.Reason + ": " + e.Message
	case e.Reason != "":
		return e.Reason
	}
	return e.Message
}

// Service is "Kind/name" of the involved object.
func (e ObjectEvent) Service() string {
	if e.Name == "" {
		return e.Kind
	}
	return e.Kind + "/" + e.Name
}
