// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package identityfillprocessor

import (
	"context"
	"maps"
	"testing"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/processor"
	"go.opentelemetry.io/collector/processor/processortest"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

const auditResourceID = "cloudaudit.googleapis.com%2Factivity"

func addResource(ld plog.Logs, attrs map[string]string) plog.ResourceLogs {
	rl := ld.ResourceLogs().AppendEmpty()
	for k, v := range attrs {
		rl.Resource().Attributes().PutStr(k, v)
	}
	rl.ScopeLogs().AppendEmpty()
	return rl
}

func addRecord(rl plog.ResourceLogs, attrs map[string]string, body string) {
	lr := rl.ScopeLogs().At(0).LogRecords().AppendEmpty()
	for k, v := range attrs {
		lr.Attributes().PutStr(k, v)
	}
	if body != "" {
		lr.Body().SetStr(body)
	}
}

func oneRecord(resAttrs, recAttrs map[string]string, body string) plog.Logs {
	ld := plog.NewLogs()
	addRecord(addResource(ld, resAttrs), recAttrs, body)
	return ld
}

func process(t *testing.T, cfg *Config, ld plog.Logs, set processor.Settings) []plog.ResourceLogs {
	t.Helper()
	sink := new(consumertest.LogsSink)
	proc, err := NewFactory().CreateLogs(context.Background(), set, cfg, sink)
	if err != nil {
		t.Fatalf("create processor: %v", err)
	}
	if err := proc.ConsumeLogs(context.Background(), ld); err != nil {
		t.Fatalf("consume: %v", err)
	}
	var out []plog.ResourceLogs
	for _, batch := range sink.AllLogs() {
		for i := 0; i < batch.ResourceLogs().Len(); i++ {
			out = append(out, batch.ResourceLogs().At(i))
		}
	}
	return out
}

func run(t *testing.T, cfg *Config, ld plog.Logs) []plog.ResourceLogs {
	t.Helper()
	return process(t, cfg, ld, processortest.NewNopSettings(component.MustNewType(typeStr)))
}

func str(m pcommon.Map, key string) string {
	if v, ok := m.Get(key); ok {
		return v.Str()
	}
	return ""
}

func hasKey(m pcommon.Map, key string) bool {
	_, ok := m.Get(key)
	return ok
}

func onlyResource(t *testing.T, got []plog.ResourceLogs) plog.ResourceLogs {
	t.Helper()
	if len(got) != 1 {
		t.Fatalf("resources = %d, want 1", len(got))
	}
	return got[0]
}

func bodyOf(rl plog.ResourceLogs) string {
	return rl.ScopeLogs().At(0).LogRecords().At(0).Body().Str()
}

func wantIdentity(t *testing.T, rl plog.ResourceLogs, host, service string) {
	t.Helper()
	attrs := rl.Resource().Attributes()
	if got := str(attrs, "host.name"); got != host {
		t.Errorf("host.name = %q, want %q", got, host)
	}
	if got := str(attrs, "service.name"); got != service {
		t.Errorf("service.name = %q, want %q", got, service)
	}
}

func TestK8sEvent_GetsClusterAsHostAndEventsAsService(t *testing.T) {
	ld := oneRecord(map[string]string{"k8s.cluster.name": "c1"}, map[string]string{"k8s.event.reason": "Pulled"}, "pulled image")

	got := onlyResource(t, run(t, defaultConfig(), ld))

	wantIdentity(t, got, "c1", "k8s-events")
	if bodyOf(got) != "pulled image" {
		t.Errorf("body = %q, want it unchanged", bodyOf(got))
	}
}

func TestPodSnapshot_WithNodeName_GetsTheNodeAsHost(t *testing.T) {
	ld := oneRecord(map[string]string{"k8s.cluster.name": "c1"},
		map[string]string{"k8s.pod.phase": "Running", "k8s.node.name": "node-1"}, "")

	got := onlyResource(t, run(t, defaultConfig(), ld))

	wantIdentity(t, got, "node-1", "k8s-pod-status")
	if str(got.Resource().Attributes(), "k8s.cluster.name") != "c1" {
		t.Error("cluster attribute lost from the resource")
	}
}

func TestPodSnapshot_WithoutNodeName_GetsTheClusterAsHost(t *testing.T) {
	ld := oneRecord(map[string]string{"k8s.cluster.name": "c1"}, map[string]string{"k8s.pod.phase": "Pending"}, "")

	got := onlyResource(t, run(t, defaultConfig(), ld))

	wantIdentity(t, got, "c1", "k8s-pod-status")
}

func TestControlPlaneLog_GetsHostAndKeepsItsService(t *testing.T) {
	ld := oneRecord(map[string]string{
		"k8s.cluster.name":   "c1",
		"k8s.container.name": "kube-apiserver",
		"service.name":       "kube-apiserver",
	}, nil, "listening")

	got := onlyResource(t, run(t, defaultConfig(), ld))

	wantIdentity(t, got, "c1", "kube-apiserver")
}

func TestGCPAudit_GetsBodyServiceAndHost(t *testing.T) {
	ld := oneRecord(
		map[string]string{"cloud.resource_id": auditResourceID, "gcp.label.cluster_name": "gke-1"},
		map[string]string{"gcp.audit.method.name": "io.k8s.core.v1.pods.get", "gcp.audit.resource.name": "core/v1/namespaces/a/pods/b"},
		"")

	got := onlyResource(t, run(t, defaultConfig(), ld))

	wantIdentity(t, got, "gke-1", "gcp-audit")
	if want := "io.k8s.core.v1.pods.get core/v1/namespaces/a/pods/b"; bodyOf(got) != want {
		t.Errorf("body = %q, want %q", bodyOf(got), want)
	}
}

func TestGCPAudit_IsRecognisedByMethodNameAlone(t *testing.T) {
	ld := oneRecord(map[string]string{"gcp.label.cluster_name": "gke-1"},
		map[string]string{"gcp.audit.method.name": "storage.objects.get"}, "")

	got := onlyResource(t, run(t, defaultConfig(), ld))

	wantIdentity(t, got, "gke-1", "gcp-audit")
}

func TestGCPAudit_BodyUsesWhicheverOfMethodAndResourceExists(t *testing.T) {
	cases := map[string]struct {
		attrs map[string]string
		want  string
	}{
		"method only":   {map[string]string{"gcp.audit.method.name": "m"}, "m"},
		"resource only": {map[string]string{"gcp.audit.resource.name": "r"}, "r"},
		"neither":       {map[string]string{}, ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			ld := oneRecord(map[string]string{"cloud.resource_id": auditResourceID}, tc.attrs, "")

			got := onlyResource(t, run(t, defaultConfig(), ld))

			if bodyOf(got) != tc.want {
				t.Errorf("body = %q, want %q", bodyOf(got), tc.want)
			}
		})
	}
}

func TestNonAuditCloudResource_IsLeftAlone(t *testing.T) {
	ld := oneRecord(map[string]string{"cloud.resource_id": "run.googleapis.com%2Fstdout"}, nil, "")

	got := onlyResource(t, run(t, defaultConfig(), ld))

	wantIdentity(t, got, "", "")
}

func TestExistingValuesAreNeverOverwritten(t *testing.T) {
	ld := oneRecord(
		map[string]string{"host.name": "kept-host", "service.name": "kept-service", "k8s.cluster.name": "c1"},
		map[string]string{"k8s.event.reason": "Pulled", "gcp.audit.method.name": "m"},
		"kept body")

	got := onlyResource(t, run(t, defaultConfig(), ld))

	wantIdentity(t, got, "kept-host", "kept-service")
	if bodyOf(got) != "kept body" {
		t.Errorf("body = %q, want it unchanged", bodyOf(got))
	}
}

func TestEmptyStringValuesCountAsMissing(t *testing.T) {
	ld := oneRecord(map[string]string{"host.name": "", "service.name": "", "k8s.cluster.name": "c1"},
		map[string]string{"k8s.event.reason": "Pulled"}, "")

	got := onlyResource(t, run(t, defaultConfig(), ld))

	wantIdentity(t, got, "c1", "k8s-events")
}

func TestRecordWithNoSignals_GetsNothing(t *testing.T) {
	ld := oneRecord(map[string]string{"deployment.environment": "dev"}, nil, "plain line")

	got := onlyResource(t, run(t, defaultConfig(), ld))

	if hasKey(got.Resource().Attributes(), "host.name") || hasKey(got.Resource().Attributes(), "service.name") {
		t.Errorf("identity added without a signal: %v", got.Resource().Attributes().AsRaw())
	}
	if bodyOf(got) != "plain line" {
		t.Errorf("body = %q, want it unchanged", bodyOf(got))
	}
}

func TestEmptyInput_ForwardsNothingAndSucceeds(t *testing.T) {
	if got := run(t, defaultConfig(), plog.NewLogs()); len(got) != 0 {
		t.Errorf("forwarded %d resources for empty input", len(got))
	}
}

func TestPodSnapshotsOnDifferentNodes_SplitIntoOneResourcePerNode(t *testing.T) {
	ld := plog.NewLogs()
	rl := addResource(ld, map[string]string{"k8s.cluster.name": "c1"})
	addRecord(rl, map[string]string{"k8s.pod.phase": "Running", "k8s.node.name": "node-1", "k8s.pod.name": "a"}, "")
	addRecord(rl, map[string]string{"k8s.pod.phase": "Running", "k8s.node.name": "node-2", "k8s.pod.name": "b"}, "")
	addRecord(rl, map[string]string{"k8s.pod.phase": "Running", "k8s.node.name": "node-1", "k8s.pod.name": "c"}, "")

	got := run(t, defaultConfig(), ld)

	if len(got) != 2 {
		t.Fatalf("resources = %d, want 2", len(got))
	}
	counts := map[string]int{}
	for _, r := range got {
		if str(r.Resource().Attributes(), "service.name") != "k8s-pod-status" || str(r.Resource().Attributes(), "k8s.cluster.name") != "c1" {
			t.Errorf("resource attributes = %v", r.Resource().Attributes().AsRaw())
		}
		counts[str(r.Resource().Attributes(), "host.name")] = r.ScopeLogs().At(0).LogRecords().Len()
	}
	if counts["node-1"] != 2 || counts["node-2"] != 1 {
		t.Errorf("records per host = %v, want node-1:2 node-2:1", counts)
	}
}

func TestMixedKindsInOneResource_SplitByService(t *testing.T) {
	ld := plog.NewLogs()
	rl := addResource(ld, map[string]string{"k8s.cluster.name": "c1"})
	addRecord(rl, map[string]string{"k8s.event.reason": "Pulled"}, "event")
	addRecord(rl, map[string]string{"k8s.pod.phase": "Running"}, "pod")

	got := run(t, defaultConfig(), ld)

	if len(got) != 2 {
		t.Fatalf("resources = %d, want 2", len(got))
	}
	byBody := map[string]plog.ResourceLogs{}
	for _, r := range got {
		byBody[bodyOf(r)] = r
	}
	wantIdentity(t, byBody["event"], "c1", "k8s-events")
	wantIdentity(t, byBody["pod"], "c1", "k8s-pod-status")
}

func TestSplit_KeepsScopesAndRecordsOfEachGroup(t *testing.T) {
	ld := plog.NewLogs()
	rl := addResource(ld, map[string]string{"k8s.cluster.name": "c1"})
	rl.SetSchemaUrl("https://example.test/schema")
	rl.ScopeLogs().At(0).Scope().SetName("scope-a")
	addRecord(rl, map[string]string{"k8s.event.reason": "Pulled"}, "event")
	addRecord(rl, map[string]string{"k8s.pod.phase": "Running"}, "pod")

	got := run(t, defaultConfig(), ld)

	if len(got) != 2 {
		t.Fatalf("resources = %d, want 2", len(got))
	}
	for _, r := range got {
		if r.SchemaUrl() != "https://example.test/schema" {
			t.Errorf("schema url = %q", r.SchemaUrl())
		}
		if sl := r.ScopeLogs().At(0); sl.Scope().Name() != "scope-a" || sl.LogRecords().Len() != 1 {
			t.Errorf("scope = %q with %d records", sl.Scope().Name(), sl.LogRecords().Len())
		}
	}
}

func TestResourcesAreHandledIndependently(t *testing.T) {
	ld := plog.NewLogs()
	addRecord(addResource(ld, map[string]string{"k8s.cluster.name": "c1"}), map[string]string{"k8s.event.reason": "Pulled"}, "")
	addRecord(addResource(ld, map[string]string{"host.name": "h2"}), map[string]string{"k8s.event.reason": "Pulled"}, "")

	got := run(t, defaultConfig(), ld)

	if len(got) != 2 {
		t.Fatalf("resources = %d, want 2", len(got))
	}
	hosts := map[string]string{}
	for _, r := range got {
		hosts[str(r.Resource().Attributes(), "host.name")] = str(r.Resource().Attributes(), "service.name")
	}
	if want := map[string]string{"c1": "k8s-events", "h2": "k8s-events"}; !maps.Equal(hosts, want) {
		t.Errorf("host to service = %v, want %v", hosts, want)
	}
}

func TestDisabledRules_AreSkipped(t *testing.T) {
	audit := func() plog.Logs {
		return oneRecord(map[string]string{"cloud.resource_id": auditResourceID, "k8s.cluster.name": "c1"},
			map[string]string{"gcp.audit.method.name": "m"}, "")
	}
	cases := map[string]struct {
		edit                func(*Config)
		host, service, body string
	}{
		"host off":    {func(c *Config) { c.HostName.Disabled = true }, "", "gcp-audit", "m"},
		"service off": {func(c *Config) { c.ServiceName.Disabled = true }, "c1", "", "m"},
		"body off":    {func(c *Config) { c.AuditBody.Disabled = true }, "c1", "gcp-audit", ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := defaultConfig()
			tc.edit(cfg)

			got := onlyResource(t, run(t, cfg, audit()))

			wantIdentity(t, got, tc.host, tc.service)
			if bodyOf(got) != tc.body {
				t.Errorf("body = %q, want %q", bodyOf(got), tc.body)
			}
		})
	}
}

func TestOverriddenServiceNames_AreUsed(t *testing.T) {
	cfg := defaultConfig()
	cfg.ServiceName.K8sEvent = "cluster-events"

	got := onlyResource(t, run(t, cfg, oneRecord(nil, map[string]string{"k8s.event.reason": "Pulled"}, "")))

	if str(got.Resource().Attributes(), "service.name") != "cluster-events" {
		t.Errorf("service.name = %q", str(got.Resource().Attributes(), "service.name"))
	}
}

func TestOverriddenHostSources_SetTheOrder(t *testing.T) {
	cfg := defaultConfig()
	cfg.HostName.Sources = []string{"k8s.cluster.name", "k8s.node.name"}
	ld := oneRecord(map[string]string{"k8s.cluster.name": "c1"}, map[string]string{"k8s.node.name": "node-1"}, "")

	got := onlyResource(t, run(t, cfg, ld))

	wantIdentity(t, got, "c1", "")
}

func filledByRule(t *testing.T, reader *sdkmetric.ManualReader) map[string]int64 {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}
	got := map[string]int64{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != filledMetric {
				continue
			}
			for _, dp := range m.Data.(metricdata.Sum[int64]).DataPoints {
				rule, _ := dp.Attributes.Value("rule")
				got[rule.AsString()] += dp.Value
			}
		}
	}
	return got
}

func TestSelfMetrics_CountFilledRecordsPerRule(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	set := processortest.NewNopSettings(component.MustNewType(typeStr))
	set.MeterProvider = sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	ld := plog.NewLogs()
	rl := addResource(ld, map[string]string{"k8s.cluster.name": "c1", "cloud.resource_id": auditResourceID})
	addRecord(rl, map[string]string{"gcp.audit.method.name": "m"}, "")
	addRecord(rl, map[string]string{"gcp.audit.method.name": "m2"}, "already set")
	addRecord(addResource(ld, map[string]string{"k8s.cluster.name": "c2", "host.name": "h"}), map[string]string{"k8s.event.reason": "Pulled"}, "")

	process(t, defaultConfig(), ld, set)

	got := filledByRule(t, reader)

	if want := map[string]int64{"host_name": 2, "service_name": 3, "audit_body": 1}; !maps.Equal(got, want) {
		t.Errorf("filled records per rule = %v, want %v", got, want)
	}
}
