// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package principal

import "testing"

func TestShorten_CloudAndKubernetesPrincipals(t *testing.T) {
	for _, c := range []struct{ name, in, want string }{
		{"gke robot", "service-135791357913@container-engine-robot.iam.gserviceaccount.com", "gke-robot"},
		{"system prefix", "system:addon-manager", "addon-manager"},
		{"system kube-controller", "system:kube-controller-manager", "kube-controller-manager"},
		{"system serviceaccount", "system:serviceaccount:kube-system:coredns", "coredns"},
		{"system node", "system:node:gk3-cluster-a-nap-1", "node-gk3-cluster-a-nap-1"},
		{"iam service account", "agent-writer@project-a.iam.gserviceaccount.com", "agent-writer"},
		{"user email", "someone@example.org", "someone"},
		{"assumed role session", "arn:aws:sts::123456789012:assumed-role/InstanceAccessRole/ssh-key-agent", "ssh-key-agent"},
		{"iam user arn", "arn:aws:iam::123456789012:user/proxmox-backup-sync", "proxmox-backup-sync"},
		{"root arn", "arn:aws:iam::123456789012:root", "root"},
		{"aws service", "ssm.amazonaws.com", "AWS Internal"},
		{"aws service cloudtrail", "cloudtrail.amazonaws.com", "AWS Internal"},
		{"plain name", "kubelet", "kubelet"},
		{"empty", "", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := Shorten(c.in, nil); got != c.want {
				t.Fatalf("Shorten(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestShorten_LongNameIsCutToTheLimit(t *testing.T) {
	got := Shorten("a-very-long-service-account-name-indeed", nil)
	if len(got) > MaxLen {
		t.Fatalf("got %q (%d bytes), limit %d", got, len(got), MaxLen)
	}
}

func TestShorten_ConfigRuleExtendsTheDefaults(t *testing.T) {
	rules := []Rule{{Contains: "custom-robot", Short: "mine"}}
	if got := Shorten("svc-1@custom-robot.example.com", rules); got != "mine" {
		t.Fatalf("got %q", got)
	}
	if got := Shorten("system:addon-manager", rules); got != "addon-manager" {
		t.Fatalf("defaults lost: %q", got)
	}
}

func TestAccountAlias_MapsIDsAndKeepsUnknownOnes(t *testing.T) {
	aliases := map[string]string{"123456789012": "account-a", "project-a": "project-a"}
	if got := AccountAlias("123456789012", aliases); got != "account-a" {
		t.Fatalf("got %q", got)
	}
	if got := AccountAlias("210987654321", aliases); got != "210987654321" {
		t.Fatalf("unknown id must stay as written, got %q", got)
	}
	if got := AccountAlias("", aliases); got != "" {
		t.Fatalf("got %q", got)
	}
	if got := AccountAlias("123456789012", nil); got != "123456789012" {
		t.Fatalf("got %q", got)
	}
}

func TestMergeAliases_ConfigReplacesPerKey(t *testing.T) {
	defaults := map[string]string{"1": "a", "2": "b"}
	got := MergeAliases(defaults, map[string]string{"2": "bb", "3": "c"})
	if got["1"] != "a" || got["2"] != "bb" || got["3"] != "c" || defaults["2"] != "b" {
		t.Fatalf("got %v defaults %v", got, defaults)
	}
}

func TestBackendService_DropsTheKubernetesHashParts(t *testing.T) {
	for in, want := range map[string]string{
		"k8s1-0a1b2c3d-grafana-grafana-3000-4e5f6a7b": "grafana-grafana-3000",
		"k8s1-deadbeef-sentry-web-9000-0123abcd":      "sentry-web-9000",
		"plain-backend":                               "plain-backend",
		"":                                            "",
	} {
		if got := BackendService(in); got != want {
			t.Errorf("BackendService(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLoadBalancerName_FromAResourceID(t *testing.T) {
	if got := LoadBalancerName("app/example-ingress/50dc6c495c0c9188"); got != "example-ingress" {
		t.Fatalf("got %q", got)
	}
	if got := LoadBalancerName("net/nlb-1/abc"); got != "nlb-1" {
		t.Fatalf("got %q", got)
	}
	if got := LoadBalancerName("plain"); got != "plain" {
		t.Fatalf("got %q", got)
	}
}
