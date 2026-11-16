// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package k8sdiscovery

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const (
	discoveryConfig = "../configs/edge-k8s-discovery.yaml"
	appsConfig      = "../configs/edge-k8s-apps.yaml"
)

// Plain maps from a YAML file, to read keys without the collector's own loader.
type doc = map[string]any

func loadDoc(t *testing.T, path string) doc {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var d doc
	if err := yaml.Unmarshal(raw, &d); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return d
}

// child returns the map at key, failing the test when it is missing or not a map.
func child(t *testing.T, parent map[string]any, key string) map[string]any {
	t.Helper()
	got, ok := parent[key].(map[string]any)
	if !ok {
		t.Fatalf("%q is missing or not a map: %v", key, parent[key])
	}
	return got
}

func TestReferenceConfig_ObserverWatchesTheLocalNodesPodsOnly(t *testing.T) {
	observerCfg := child(t, child(t, loadDoc(t, discoveryConfig), "extensions"), "k8s_observer")

	if observerCfg["node"] != "${env:K8S_NODE_NAME}" {
		t.Errorf("node = %v, want ${env:K8S_NODE_NAME}", observerCfg["node"])
	}
	if observerCfg["observe_pods"] != true {
		t.Errorf("observe_pods = %v, want true", observerCfg["observe_pods"])
	}
	for key := range observerCfg {
		if strings.HasPrefix(key, "observe_") && key != "observe_pods" {
			t.Errorf("%s is set: the reference observes pods only", key)
		}
	}
}

func TestReferenceConfig_OnePrometheusRuleThatLeavesOutTheCollectorsOwnPod(t *testing.T) {
	creator := child(t, child(t, loadDoc(t, discoveryConfig), "receivers"), "receiver_creator/pods")
	rules := child(t, creator, "receivers")

	if len(rules) != 1 {
		t.Fatalf("got %d rules, want exactly one", len(rules))
	}
	prom := child(t, rules, "prometheus")
	rule, _ := prom["rule"].(string)
	if !strings.Contains(rule, `labels["app.kubernetes.io/component"] != "opentelemetry-collector"`) {
		t.Errorf("rule %q does not leave out the collector's own pod", rule)
	}
}

func TestReferenceConfig_PodLabelsAreAddedAfterTheCreatorAssociatedOnThePodUID(t *testing.T) {
	d := loadDoc(t, discoveryConfig)
	pipeline := child(t, child(t, child(t, d, "service"), "pipelines"), "metrics")

	processors, _ := pipeline["processors"].([]any)
	found := false
	for _, p := range processors {
		found = found || p == "k8s_attributes"
	}
	if !found {
		t.Errorf("processors = %v, want k8s_attributes", processors)
	}
	receivers, _ := pipeline["receivers"].([]any)
	if len(receivers) != 1 || receivers[0] != "receiver_creator/pods" {
		t.Errorf("receivers = %v, want the creator only", receivers)
	}
	association, _ := child(t, child(t, d, "processors"), "k8s_attributes")["pod_association"].([]any)
	if len(association) != 1 {
		t.Fatalf("pod_association = %v, want one entry", association)
	}
	sources, _ := association[0].(map[string]any)["sources"].([]any)
	source, _ := sources[0].(map[string]any)
	if source["name"] != "k8s.pod.uid" || source["from"] != "resource_attribute" {
		t.Errorf("association source = %v, want resource_attribute k8s.pod.uid", source)
	}
}

func TestReferenceConfig_KeepsAnnotationDiscoveryOff(t *testing.T) {
	for _, path := range []string{discoveryConfig, appsConfig} {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "discovery:") {
			t.Errorf("%s has a discovery block; annotation discovery stays off", path)
		}
	}
}

func TestAppsConfig_HasOneRuleForEachOfTheSixReceiversAndNoOthers(t *testing.T) {
	rules := child(t, child(t, child(t, loadDoc(t, appsConfig), "receivers"), "receiver_creator/apps"), "receivers")

	want := []string{"redis", "postgresql", "mysql", "mongodb", "rabbitmq", "nginx"}
	if len(rules) != len(want) {
		t.Errorf("got %d rules, want %d", len(rules), len(want))
	}
	for _, name := range want {
		if _, ok := rules[name]; !ok {
			t.Errorf("no rule for %s", name)
		}
	}
	for _, banned := range []string{"memcached", "kafka_metrics"} {
		if _, ok := rules[banned]; ok {
			t.Errorf("a rule for %s: that receiver is not compiled in", banned)
		}
	}
}

func TestAppsConfig_CredentialsComeFromTheEnvironmentOnly(t *testing.T) {
	raw, err := os.ReadFile(appsConfig)
	if err != nil {
		t.Fatal(err)
	}
	for number, line := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		for _, key := range []string{"password:", "username:"} {
			if !strings.HasPrefix(trimmed, key) {
				continue
			}
			value := strings.TrimSpace(strings.TrimPrefix(trimmed, key))
			if !strings.HasPrefix(value, "${env:") && !strings.HasPrefix(value, "${file:") {
				t.Errorf("line %d: %s is not read from the environment or a file", number+1, trimmed)
			}
		}
	}
}
