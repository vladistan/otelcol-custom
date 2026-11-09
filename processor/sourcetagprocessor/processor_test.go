// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package sourcetagprocessor

import (
	"context"
	"reflect"
	"testing"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/processor"
	"go.opentelemetry.io/collector/processor/processortest"
)

// input describes one record with its resource and scope attributes.
type input struct {
	res, scope, attrs map[string]string
	body              string
	severity          plog.SeverityNumber
	severityText      string
}

func (in input) logs() plog.Logs {
	ld := plog.NewLogs()
	rl := ld.ResourceLogs().AppendEmpty()
	for k, v := range in.res {
		rl.Resource().Attributes().PutStr(k, v)
	}
	sl := rl.ScopeLogs().AppendEmpty()
	for k, v := range in.scope {
		sl.Scope().Attributes().PutStr(k, v)
	}
	lr := sl.LogRecords().AppendEmpty()
	for k, v := range in.attrs {
		lr.Attributes().PutStr(k, v)
	}
	if in.body != "" {
		lr.Body().SetStr(in.body)
	}
	lr.SetSeverityNumber(in.severity)
	lr.SetSeverityText(in.severityText)
	return ld
}

func newProc(t *testing.T, provider string, sink *consumertest.LogsSink, set processor.Settings) processor.Logs {
	t.Helper()
	proc, err := NewFactory().CreateLogs(context.Background(), set, &Config{Provider: provider}, sink)
	if err != nil {
		t.Fatalf("create processor: %v", err)
	}
	return proc
}

func nopSettings() processor.Settings {
	return processortest.NewNopSettings(component.MustNewType(typeStr))
}

func consume(t *testing.T, proc processor.Logs, ld plog.Logs) {
	t.Helper()
	if err := proc.ConsumeLogs(context.Background(), ld); err != nil {
		t.Fatalf("consume: %v", err)
	}
}

// tag runs the input through one processor and returns the single output resource and record.
func tag(t *testing.T, provider string, in input) (pcommon.Map, plog.LogRecord) {
	t.Helper()
	sink := new(consumertest.LogsSink)
	consume(t, newProc(t, provider, sink, nopSettings()), in.logs())
	if sink.LogRecordCount() != 1 {
		t.Fatalf("records out = %d, want 1", sink.LogRecordCount())
	}
	rl := sink.AllLogs()[0].ResourceLogs().At(0)
	return rl.Resource().Attributes(), rl.ScopeLogs().At(0).LogRecords().At(0)
}

func wantStr(t *testing.T, m pcommon.Map, key, want string) {
	t.Helper()
	got, ok := m.Get(key)
	if !ok || got.Str() != want {
		t.Errorf("%s = %q (present=%v), want %q", key, got.Str(), ok, want)
	}
}

func wantAbsent(t *testing.T, m pcommon.Map, key string) {
	t.Helper()
	if v, ok := m.Get(key); ok {
		t.Errorf("%s = %v, want it absent", key, v.AsRaw())
	}
}

var (
	cloudTrail = map[string]string{"encoding.format": "aws.cloudtrail"}
	ekGroup    = map[string]string{"cloudwatch.log.group.name": "/aws/eks/prod/cluster"}
)

func TestAWS_CloudTrailScopeSetsSourceAndCategory(t *testing.T) {
	_, lr := tag(t, "aws", input{scope: cloudTrail})

	wantStr(t, lr.Attributes(), "log.source", "aws.cloudtrail")
	wantStr(t, lr.Attributes(), "event.category", "audit")
}

func TestAWS_CloudTrailBodyIsRPCMethodWithEverySlashReplaced(t *testing.T) {
	_, lr := tag(t, "aws", input{scope: cloudTrail, attrs: map[string]string{"rpc.method": "ec2.amazonaws.com/a/b"}})

	if got := lr.Body().Str(); got != "ec2.amazonaws.com a b" {
		t.Errorf("body = %q, want %q", got, "ec2.amazonaws.com a b")
	}
}

func TestAWS_CloudTrailWithoutRPCMethodEmptiesTheBody(t *testing.T) {
	_, lr := tag(t, "aws", input{scope: cloudTrail, body: "x/y"})

	if lr.Body().Type() != pcommon.ValueTypeEmpty {
		t.Errorf("body = %v, want empty as the replaced rule leaves it", lr.Body().AsRaw())
	}
}

func TestAWS_CloudTrailErrorCodeRaisesSeverityToWarn(t *testing.T) {
	_, lr := tag(t, "aws", input{scope: cloudTrail, attrs: map[string]string{"aws.error.code": "AccessDenied"}})

	if lr.SeverityNumber() != plog.SeverityNumberWarn || lr.SeverityText() != "WARN" {
		t.Errorf("severity = %v/%q, want WARN/WARN", lr.SeverityNumber(), lr.SeverityText())
	}
}

func TestAWS_CloudTrailWithoutErrorCodeIsInfo(t *testing.T) {
	_, lr := tag(t, "aws", input{scope: cloudTrail})

	if lr.SeverityNumber() != plog.SeverityNumberInfo || lr.SeverityText() != "INFO" {
		t.Errorf("severity = %v/%q, want INFO/INFO", lr.SeverityNumber(), lr.SeverityText())
	}
}

func TestAWS_S3AccessScopeSetsSourceAndAccessCategory(t *testing.T) {
	_, lr := tag(t, "aws", input{scope: map[string]string{"encoding.format": "aws.s3access"}})

	wantStr(t, lr.Attributes(), "log.source", "aws.s3access")
	wantStr(t, lr.Attributes(), "event.category", "access")
}

func TestAWS_ELBScopeSetsSourceAndAccessCategory(t *testing.T) {
	_, lr := tag(t, "aws", input{scope: map[string]string{"encoding.format": "aws.elbaccess"}})

	wantStr(t, lr.Attributes(), "log.source", "aws.elb")
	wantStr(t, lr.Attributes(), "event.category", "access")
}

func TestAWS_EKSAuditStreamIsAuditCategory(t *testing.T) {
	res := map[string]string{"cloudwatch.log.stream": "kube-apiserver-audit-abc"}
	for k, v := range ekGroup {
		res[k] = v
	}

	_, lr := tag(t, "aws", input{res: res})

	wantStr(t, lr.Attributes(), "log.source", "aws.eks")
	wantStr(t, lr.Attributes(), "event.category", "audit")
}

func TestAWS_EKSOtherStreamIsApplicationCategory(t *testing.T) {
	res := map[string]string{"cloudwatch.log.stream": "authenticator-abc"}
	for k, v := range ekGroup {
		res[k] = v
	}

	_, lr := tag(t, "aws", input{res: res})

	wantStr(t, lr.Attributes(), "log.source", "aws.eks")
	wantStr(t, lr.Attributes(), "event.category", "application")
}

func TestAWS_EKSWithoutStreamIsApplicationCategory(t *testing.T) {
	_, lr := tag(t, "aws", input{res: ekGroup})

	wantStr(t, lr.Attributes(), "event.category", "application")
}

func TestAWS_EKSKeepsACategoryTheRecordAlreadyHas(t *testing.T) {
	_, lr := tag(t, "aws", input{res: ekGroup, attrs: map[string]string{"event.category": "security"}})

	wantStr(t, lr.Attributes(), "event.category", "security")
}

func TestAWS_OtherCloudWatchGroupIsCloudWatchApplication(t *testing.T) {
	_, lr := tag(t, "aws", input{res: map[string]string{"cloudwatch.log.group.name": "/aws/lambda/api"}})

	wantStr(t, lr.Attributes(), "log.source", "aws.cloudwatch")
	wantStr(t, lr.Attributes(), "event.category", "application")
}

func TestAWS_RecordWithoutGroupOrScopeGetsNoSource(t *testing.T) {
	_, lr := tag(t, "aws", input{})

	wantAbsent(t, lr.Attributes(), "log.source")
	wantAbsent(t, lr.Attributes(), "event.category")
}

func TestAWS_PresetSourceIsKeptOverAGroup(t *testing.T) {
	_, lr := tag(t, "aws", input{res: ekGroup, attrs: map[string]string{"log.source": "auth0"}})

	wantStr(t, lr.Attributes(), "log.source", "auth0")
	wantAbsent(t, lr.Attributes(), "event.category")
}

func TestAWS_PresetSeverityIsKept(t *testing.T) {
	_, lr := tag(t, "aws", input{severity: plog.SeverityNumberError, severityText: "ERROR"})

	if lr.SeverityNumber() != plog.SeverityNumberError || lr.SeverityText() != "ERROR" {
		t.Errorf("severity = %v/%q, want ERROR/ERROR", lr.SeverityNumber(), lr.SeverityText())
	}
}

func TestAWS_SeverityTextIsWrittenOnlyForInfoAndWarn(t *testing.T) {
	_, lr := tag(t, "aws", input{severity: plog.SeverityNumberError, severityText: "boom"})

	if got := lr.SeverityText(); got != "boom" {
		t.Errorf("severity text = %q, want it kept", got)
	}
}

func TestAWS_PresetInfoNumberGetsInfoText(t *testing.T) {
	_, lr := tag(t, "aws", input{severity: plog.SeverityNumberInfo, severityText: "notice"})

	if got := lr.SeverityText(); got != "INFO" {
		t.Errorf("severity text = %q, want INFO", got)
	}
}

func TestAWS_RegionFillsProviderAndRegion(t *testing.T) {
	res, _ := tag(t, "aws", input{res: map[string]string{"aws.region": "us-east-1"}})

	wantStr(t, res, "cloud.provider", "aws")
	wantStr(t, res, "cloud.region", "us-east-1")
}

func TestAWS_NoRegionLeavesResourceAlone(t *testing.T) {
	res, _ := tag(t, "aws", input{})

	wantAbsent(t, res, "cloud.provider")
	wantAbsent(t, res, "cloud.region")
}

func TestAWS_ExistingProviderAndRegionAreKept(t *testing.T) {
	res, _ := tag(t, "aws", input{res: map[string]string{"aws.region": "us-east-1", "cloud.provider": "x", "cloud.region": "y"}})

	wantStr(t, res, "cloud.provider", "x")
	wantStr(t, res, "cloud.region", "y")
}

func TestGCP_ResourceTakesLabelsAndSetsProvider(t *testing.T) {
	res, _ := tag(t, "gcp", input{res: map[string]string{
		"gcp.label.service_name": "api", "gcp.label.location": "us-central1", "gcp.label.project_id": "p1",
	}})

	wantStr(t, res, "cloud.provider", "gcp")
	wantStr(t, res, "service.name", "api")
	wantStr(t, res, "cloud.region", "us-central1")
	wantStr(t, res, "cloud.account.id", "p1")
}

func TestGCP_ProviderIsSetOverAnExistingValue(t *testing.T) {
	res, _ := tag(t, "gcp", input{res: map[string]string{"cloud.provider": "other"}})

	wantStr(t, res, "cloud.provider", "gcp")
}

func TestGCP_ProjectFillsAccountWhenNoLabelDoes(t *testing.T) {
	res, _ := tag(t, "gcp", input{res: map[string]string{"gcp.project": "p2"}})

	wantStr(t, res, "cloud.account.id", "p2")
}

func TestGCP_LabelAccountWinsOverProject(t *testing.T) {
	res, _ := tag(t, "gcp", input{res: map[string]string{"gcp.label.project_id": "p1", "gcp.project": "p2"}})

	wantStr(t, res, "cloud.account.id", "p1")
}

func TestGCP_ResourceWithoutLabelsGetsOnlyProvider(t *testing.T) {
	res, _ := tag(t, "gcp", input{})

	wantStr(t, res, "cloud.provider", "gcp")
	wantAbsent(t, res, "service.name")
	wantAbsent(t, res, "cloud.region")
	wantAbsent(t, res, "cloud.account.id")
}

func TestGCP_AuditResourceIDSetsSourceAndAuditCategory(t *testing.T) {
	_, lr := tag(t, "gcp", input{res: map[string]string{"cloud.resource_id": "cloudaudit.googleapis.com%2Factivity"}})

	wantStr(t, lr.Attributes(), "log.source", "gcp.audit")
	wantStr(t, lr.Attributes(), "event.category", "audit")
}

func TestGCP_AuditOverwritesAPresetSource(t *testing.T) {
	_, lr := tag(t, "gcp", input{
		res:   map[string]string{"cloud.resource_id": "cloudaudit.googleapis.com%2Factivity"},
		attrs: map[string]string{"log.source": "other"},
	})

	wantStr(t, lr.Attributes(), "log.source", "gcp.audit")
}

func TestGCP_CloudRunSetsSourceAndApplicationCategory(t *testing.T) {
	_, lr := tag(t, "gcp", input{res: map[string]string{"gcp.resource_type": "cloud_run_revision"}})

	wantStr(t, lr.Attributes(), "log.source", "gcp.cloudrun")
	wantStr(t, lr.Attributes(), "event.category", "application")
}

func TestGCP_CloudRunKeepsAPresetSource(t *testing.T) {
	_, lr := tag(t, "gcp", input{
		res:   map[string]string{"gcp.resource_type": "cloud_run_revision"},
		attrs: map[string]string{"log.source": "other"},
	})

	wantStr(t, lr.Attributes(), "log.source", "other")
	wantAbsent(t, lr.Attributes(), "event.category")
}

func TestGCP_EachLoadBalancerTypeSetsLBSourceAndAccessCategory(t *testing.T) {
	for _, typ := range []string{"http_load_balancer", "l7_lb_rule", "https_lb_rule"} {
		t.Run(typ, func(t *testing.T) {
			_, lr := tag(t, "gcp", input{res: map[string]string{"gcp.resource_type": typ}})

			wantStr(t, lr.Attributes(), "log.source", "gcp.lb")
			wantStr(t, lr.Attributes(), "event.category", "access")
		})
	}
}

func TestGCP_LoadBalancerTypeMatchIsAnchored(t *testing.T) {
	_, lr := tag(t, "gcp", input{res: map[string]string{"gcp.resource_type": "http_load_balancer_v2"}})

	wantAbsent(t, lr.Attributes(), "log.source")
}

func TestGCP_UnmatchedResourceTypeGetsNoSource(t *testing.T) {
	_, lr := tag(t, "gcp", input{res: map[string]string{"gcp.resource_type": "gce_instance"}})

	wantAbsent(t, lr.Attributes(), "log.source")
}

func TestAzure_SetsProviderSourceAndAuditCategory(t *testing.T) {
	res, lr := tag(t, "azure", input{attrs: map[string]string{"log.source": "other"}})

	wantStr(t, res, "cloud.provider", "azure")
	wantStr(t, lr.Attributes(), "log.source", "azure.activity")
	wantStr(t, lr.Attributes(), "event.category", "audit")
}

func TestTagging_TwiceEqualsOnce(t *testing.T) {
	in := input{
		scope: cloudTrail,
		res:   map[string]string{"aws.region": "us-east-1"},
		attrs: map[string]string{"rpc.method": "iam.amazonaws.com/CreateUser", "aws.error.code": "X"},
	}
	sink := new(consumertest.LogsSink)
	proc := newProc(t, "aws", sink, nopSettings())
	ld := in.logs()
	consume(t, proc, ld)
	once := plog.NewLogs()
	ld.CopyTo(once)

	consume(t, proc, ld)

	if got, want := snapshot(ld), snapshot(once); !reflect.DeepEqual(got, want) {
		t.Errorf("second pass changed the output: %v vs %v", got, want)
	}
}

// snapshot flattens the one record of ld with its resource attributes.
func snapshot(ld plog.Logs) []any {
	rl := ld.ResourceLogs().At(0)
	lr := rl.ScopeLogs().At(0).LogRecords().At(0)
	return []any{rl.Resource().Attributes().AsRaw(), lr.Attributes().AsRaw(), lr.Body().AsRaw(), lr.SeverityNumber(), lr.SeverityText()}
}
