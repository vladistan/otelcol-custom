// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package bench

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/processor"
	"go.opentelemetry.io/collector/processor/processortest"
	"gopkg.in/yaml.v3"

	"github.com/vladistan/otelcol-custom/processor/k8seventprocessor"
)

const (
	kubeV0Values     = "../../processor/k8seventprocessor/testdata/kube-v0-values-cluster.yaml"
	appsEKSV0Values  = "../../processor/k8seventprocessor/testdata/apps-eks-v0-values-cluster.yaml"
	siteValues       = "../../processor/k8seventprocessor/testdata/site-k8s-values-cluster.yaml"
	k8sBodies        = "../../processor/k8seventprocessor/testdata/k8sobjects-bodies.json"
	k8sClusterBodies = "../../processor/k8seventprocessor/testdata/k8sobjects-bodies-clusters.json"
	k8sOTTLBlock     = "transform/k8s_objects"
)

// k8sOTTLKeys lists every attribute the transform/k8s_objects block can set.
var k8sOTTLKeys = []string{
	"k8s.object.kind", "k8s.object.action", "k8s.namespace", "k8s.name",
	"k8s.event.type", "k8s.event.reason", "k8s.event.note", "k8s.event.count",
	"k8s.event.regarding.kind", "k8s.event.regarding.name", "k8s.event.regarding.namespace",
	"k8s.pod.phase", "k8s.container.exit_code", "k8s.container.state",
	"k8s.labels.app", "k8s.labels.environment", "k8s.labels.job_name",
	"message_type", "message", "k8s.object.full",
}

type k8sCase struct {
	Name string         `json:"name"`
	Body map[string]any `json:"body"`
}

// loadK8sCases returns the kube-v0 bodies followed by the apps-eks-v0 and site-k8s bodies.
func loadK8sCases(tb testing.TB) []k8sCase {
	tb.Helper()
	return append(loadK8sCasesFrom(tb, k8sBodies), loadK8sCasesFrom(tb, k8sClusterBodies)...)
}

func loadK8sCasesFrom(tb testing.TB, path string) []k8sCase {
	tb.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		tb.Fatalf("read bodies: %v", err)
	}
	var cases []k8sCase
	if err := json.Unmarshal(raw, &cases); err != nil {
		tb.Fatalf("parse %s: %v", path, err)
	}
	return cases
}

func loadK8sValuesProcessors(tb testing.TB, path string) map[string]map[string]any {
	tb.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		tb.Fatalf("read %s: %v", path, err)
	}
	var doc struct {
		Config struct {
			Processors map[string]map[string]any `yaml:"processors"`
		} `yaml:"config"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		tb.Fatalf("parse %s: %v", path, err)
	}
	return doc.Config.Processors
}

func newK8sOTTL(tb testing.TB, next consumer.Logs) processor.Logs {
	tb.Helper()
	return newK8sOTTLFrom(tb, next, kubeV0Values)
}

func newK8sOTTLFrom(tb testing.TB, next consumer.Logs, valuesPath string) processor.Logs {
	tb.Helper()
	block, ok := loadK8sValuesProcessors(tb, valuesPath)[k8sOTTLBlock]
	if !ok {
		tb.Fatalf("%s not found in %s", k8sOTTLBlock, valuesPath)
	}
	return newOTTLFromBlock(tb, next, block)
}

func newK8sEvent(tb testing.TB, next consumer.Logs, mutate func(*k8seventprocessor.Config)) processor.Logs {
	tb.Helper()
	f := k8seventprocessor.NewFactory()
	cfg := f.CreateDefaultConfig().(*k8seventprocessor.Config)
	cfg.OTTLParity = true
	if mutate != nil {
		mutate(cfg)
	}
	p, err := f.CreateLogs(context.Background(), processortest.NewNopSettings(f.Type()), cfg, next)
	if err != nil {
		tb.Fatalf("create k8seventprocessor: %v", err)
	}
	return p
}

func TestK8sEventParity_EveryOTTLFieldMatches(t *testing.T) {
	ottlSink, k8sSink := new(consumertest.LogsSink), new(consumertest.LogsSink)
	ottl, k8s := newK8sOTTL(t, ottlSink), newK8sEvent(t, k8sSink, nil)

	for _, c := range loadK8sCases(t) {
		t.Run(c.Name, func(t *testing.T) {
			_, want := singleRecord(t, runThrough(t, ottl, ottlSink, batchOf(t, c.Body)))
			_, got := singleRecord(t, runThrough(t, k8s, k8sSink, batchOf(t, c.Body)))

			assertK8sRecordsMatch(t, want, got)
		})
	}
}

// assertK8sRecordsMatch compares every field the OTTL block sets with the k8sevent output.
func assertK8sRecordsMatch(t *testing.T, want, got plog.LogRecord) {
	t.Helper()
	for _, key := range k8sOTTLKeys {
		wantV, wantOK := want.Attributes().Get(key)
		gotV, gotOK := got.Attributes().Get(key)
		if wantOK != gotOK {
			t.Errorf("%s: OTTL present=%v, k8sevent present=%v", key, wantOK, gotOK)
			continue
		}
		if wantOK && !reflect.DeepEqual(wantV.AsRaw(), gotV.AsRaw()) {
			t.Errorf("%s = %v, OTTL gave %v", key, gotV.AsRaw(), wantV.AsRaw())
		}
	}
	if got.Body().AsRaw() == nil || !reflect.DeepEqual(got.Body().AsRaw(), want.Body().AsRaw()) {
		t.Errorf("body = %v, OTTL gave %v", got.Body().AsRaw(), want.Body().AsRaw())
	}
	// The block sets severity only for Warning, Normal and Pod; otherwise adjust_severity may fill it.
	if want.SeverityText() != "" &&
		(got.SeverityNumber() != want.SeverityNumber() || got.SeverityText() != want.SeverityText()) {
		t.Errorf("severity = %v/%q, OTTL gave %v/%q",
			got.SeverityNumber(), got.SeverityText(), want.SeverityNumber(), want.SeverityText())
	}
}

func TestK8sEventParity_EachClusterBlockMatchesOnEveryBody(t *testing.T) {
	for _, values := range []string{kubeV0Values, appsEKSV0Values, siteValues} {
		t.Run(filepath.Base(values), func(t *testing.T) {
			ottlSink, k8sSink := new(consumertest.LogsSink), new(consumertest.LogsSink)
			ottl, k8s := newK8sOTTLFrom(t, ottlSink, values), newK8sEvent(t, k8sSink, nil)

			for _, c := range loadK8sCases(t) {
				t.Run(c.Name, func(t *testing.T) {
					_, want := singleRecord(t, runThrough(t, ottl, ottlSink, batchOf(t, c.Body)))
					_, got := singleRecord(t, runThrough(t, k8s, k8sSink, batchOf(t, c.Body)))

					assertK8sRecordsMatch(t, want, got)
				})
			}
		})
	}
}

func TestClusterValues_ResourceClusterBlockSetsTheClusterName(t *testing.T) {
	for values, want := range map[string]string{
		appsEKSV0Values: "apps-eks-v0",
		siteValues:      "site-k8s",
	} {
		t.Run(want, func(t *testing.T) {
			block, ok := loadK8sValuesProcessors(t, values)["resource/cluster"]
			if !ok {
				t.Fatalf("resource/cluster not found in %s", values)
			}
			var got string
			for _, a := range block["attributes"].([]any) {
				attr := a.(map[string]any)
				if attr["key"] == "k8s.cluster.name" {
					got, _ = attr["value"].(string)
				}
			}
			if got != want {
				t.Errorf("k8s.cluster.name = %q, want %q", got, want)
			}
		})
	}
}

func TestK8sEventParity_HoldsWithOldSwitchesOff(t *testing.T) {
	ottlSink, k8sSink := new(consumertest.LogsSink), new(consumertest.LogsSink)
	ottl := newK8sOTTL(t, ottlSink)
	k8s := newK8sEvent(t, k8sSink, func(c *k8seventprocessor.Config) { c.RewriteMessages, c.AdjustSeverity = false, false })

	for _, c := range loadK8sCases(t) {
		_, want := singleRecord(t, runThrough(t, ottl, ottlSink, batchOf(t, c.Body)))
		_, got := singleRecord(t, runThrough(t, k8s, k8sSink, batchOf(t, c.Body)))

		if !reflect.DeepEqual(got.Body().AsRaw(), want.Body().AsRaw()) ||
			got.SeverityNumber() != want.SeverityNumber() || got.SeverityText() != want.SeverityText() {
			t.Errorf("%s: body/severity differ from OTTL with the old switches off", c.Name)
		}
	}
}

func TestK8sEventParity_OtherKindsPassUnchanged(t *testing.T) {
	sink := new(consumertest.LogsSink)
	k8s := newK8sEvent(t, sink, nil)
	body := map[string]any{"type": "ADDED", "object": map[string]any{
		"kind": "Deployment", "metadata": map[string]any{"name": "d", "namespace": "n"},
	}}

	_, got := singleRecord(t, runThrough(t, k8s, sink, batchOf(t, body)))

	if got.Attributes().Len() != 0 || got.SeverityNumber() != plog.SeverityNumberUnspecified {
		t.Errorf("Deployment record changed: %v", got.Attributes().AsRaw())
	}
	if !reflect.DeepEqual(got.Body().AsRaw(), map[string]any(body)) {
		t.Errorf("Deployment body changed: %v", got.Body().AsRaw())
	}
}
