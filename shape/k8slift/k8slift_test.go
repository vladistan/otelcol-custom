// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

// cspell:ignore slift

package k8slift

import (
	"testing"

	"go.opentelemetry.io/collector/pdata/pcommon"
)

func raw(t *testing.T, kv map[string]any) pcommon.Map {
	t.Helper()
	m := pcommon.NewMap()
	if err := m.FromRaw(kv); err != nil {
		t.Fatal(err)
	}
	return m
}

func str(t *testing.T, m pcommon.Map, key string) string {
	t.Helper()
	v, ok := m.Get(key)
	if !ok {
		t.Fatalf("%s missing; have %v", key, m.AsRaw())
	}
	return v.Str()
}

func TestLift_MovesNamespacePodAndNodeIntoTheResource(t *testing.T) {
	res := raw(t, map[string]any{"cloud.provider": "gcp"})
	rec := raw(t, map[string]any{"k8s.namespace.name": "billing-sync", "k8s.pod.name": "billing-sync-abc", "k8s.node.name": "k8s-2", "other": "x"})
	Lift(res, rec)
	if str(t, res, "k8s.namespace.name") != "billing-sync" || str(t, res, "k8s.pod.name") != "billing-sync-abc" || str(t, res, "k8s.node.name") != "k8s-2" {
		t.Fatalf("resource = %v", res.AsRaw())
	}
	if _, ok := rec.Get("k8s.pod.name"); ok {
		t.Fatalf("the record still holds the lifted key: %v", rec.AsRaw())
	}
	if str(t, rec, "other") != "x" {
		t.Fatal("unrelated attribute lost")
	}
}

func TestLift_ShortNamesAreAccepted(t *testing.T) {
	res := pcommon.NewMap()
	rec := raw(t, map[string]any{"namespace": "kube-system", "pod": "coredns-1", "node": "k8s-1"})
	Lift(res, rec)
	if str(t, res, "k8s.namespace.name") != "kube-system" || str(t, res, "k8s.pod.name") != "coredns-1" || str(t, res, "k8s.node.name") != "k8s-1" {
		t.Fatalf("resource = %v", res.AsRaw())
	}
}

func TestLift_ResourceValueWins(t *testing.T) {
	res := raw(t, map[string]any{"k8s.pod.name": "from-resource"})
	rec := raw(t, map[string]any{"k8s.pod.name": "from-record"})
	Lift(res, rec)
	if str(t, res, "k8s.pod.name") != "from-resource" {
		t.Fatalf("resource = %v", res.AsRaw())
	}
}

func TestLift_HostNameComesFromTheNode(t *testing.T) {
	res := raw(t, map[string]any{"k8s.node.name": "k8s-2"})
	Lift(res, pcommon.NewMap())
	if str(t, res, "host.name") != "k8s-2" {
		t.Fatalf("resource = %v", res.AsRaw())
	}
	res = raw(t, map[string]any{"k8s.node.name": "k8s-2", "host.name": "kept"})
	Lift(res, pcommon.NewMap())
	if str(t, res, "host.name") != "kept" {
		t.Fatalf("resource = %v", res.AsRaw())
	}
}

func TestParsePodStatus_ReadsTheStatusLine(t *testing.T) {
	st, ok := ParsePodStatus("Pod billing-sync/billing-sync-gcp-prod-29918700-abcde: Succeeded (0/1 ready) on k8s-2")
	if !ok {
		t.Fatal("not parsed")
	}
	if st.Namespace != "billing-sync" || st.Pod != "billing-sync-gcp-prod-29918700-abcde" || st.Phase != "Succeeded" || st.Ready != "0/1" || st.Node != "k8s-2" {
		t.Fatalf("got %+v", st)
	}
	if got := st.Text(); got != "Succeeded (0/1 ready)" {
		t.Fatalf("Text = %q", got)
	}
}

func TestParsePodStatus_NotAStatusLine(t *testing.T) {
	for _, in := range []string{"", "hello", "Pod without colon", "Pod single: Running", "Pod a/b:"} {
		if _, ok := ParsePodStatus(in); ok {
			t.Errorf("%q must not parse", in)
		}
	}
}

func TestParsePodStatus_NoReadyNoNode(t *testing.T) {
	st, ok := ParsePodStatus("Pod a/b: Pending")
	if !ok || st.Phase != "Pending" || st.Ready != "" || st.Node != "" || st.Text() != "Pending" {
		t.Fatalf("got %+v %v", st, ok)
	}
}

func TestNodeFromStaticPod_StripsTheComponentPrefix(t *testing.T) {
	for _, c := range []struct{ pod, want string }{
		{"kube-apiserver-k8s-1", "k8s-1"},
		{"kube-controller-manager-k8s-2", "k8s-2"},
		{"kube-scheduler-k8s-3", "k8s-3"},
		{"etcd-k8s-1", "k8s-1"},
		{"coredns-5d78c9869d-abcde", ""},
		{"kube-apiserver-", ""},
		{"", ""},
	} {
		got, ok := NodeFromStaticPod(c.pod, nil)
		if got != c.want || ok != (c.want != "") {
			t.Errorf("NodeFromStaticPod(%q) = %q, %v; want %q", c.pod, got, ok, c.want)
		}
	}
}

func TestNodeFromStaticPod_ConfigPrefixesExtendTheDefaults(t *testing.T) {
	got, ok := NodeFromStaticPod("kube-vip-k8s-1", []string{"kube-vip-"})
	if !ok || got != "k8s-1" {
		t.Fatalf("got %q %v", got, ok)
	}
	if got, ok := NodeFromStaticPod("etcd-k8s-1", []string{"kube-vip-"}); !ok || got != "k8s-1" {
		t.Fatalf("defaults lost: %q %v", got, ok)
	}
}

func TestParseObjectEvent_ReadsTheKubernetesEventJSON(t *testing.T) {
	const doc = `{"involvedObject":{"kind":"RedirectService","name":"default","namespace":"kube-system"},"reason":"UpdateRedirectService","message":"deleted redirect service obj default","type":"Normal","source":{"host":"gk3-cluster-a-nap-1"}}`
	ev, ok := ParseObjectEvent(doc)
	if !ok {
		t.Fatal("not parsed")
	}
	if ev.Kind != "RedirectService" || ev.Name != "default" || ev.Namespace != "kube-system" || ev.Reason != "UpdateRedirectService" ||
		ev.Message != "deleted redirect service obj default" || ev.Type != "Normal" || ev.Host != "gk3-cluster-a-nap-1" {
		t.Fatalf("got %+v", ev)
	}
	if ev.Text() != "UpdateRedirectService: deleted redirect service obj default" {
		t.Fatalf("Text = %q", ev.Text())
	}
	if ev.Service() != "RedirectService/default" {
		t.Fatalf("Service = %q", ev.Service())
	}
}

func TestParseObjectEvent_NotJSONOrNotAnEvent(t *testing.T) {
	for _, in := range []string{"", "plain text", `{"a":1}`, `{"reason":"x"`, `[1,2]`} {
		if _, ok := ParseObjectEvent(in); ok {
			t.Errorf("%q must not parse", in)
		}
	}
}
