// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

// cspell:ignore Fdata

package sourcetagprocessor

import (
	"testing"

	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/pdata/plog"
)

// Values come from documentation examples: us-east-2, account 123456789012, project example-project.

func TestCloudServiceName_FollowsTheSourceOfTheRecords(t *testing.T) {
	cases := []struct {
		name     string
		provider string
		in       input
		want     string
	}{
		{"cloudtrail", "aws", input{scope: map[string]string{"encoding.format": "aws.cloudtrail"}}, "cloudtrail"},
		{"s3 access", "aws", input{scope: map[string]string{"encoding.format": "aws.s3access"}}, "s3"},
		{"elb", "aws", input{scope: map[string]string{"encoding.format": "aws.elbaccess"}}, "elasticloadbalancing"},
		{"eks", "aws", input{res: map[string]string{"cloudwatch.log.group.name": "/aws/eks/prod/cluster"}}, "eks"},
		{"cloudwatch logs", "aws", input{res: map[string]string{"cloudwatch.log.group.name": "/aws/lambda/api"}}, "logs"},
		{"auth0", "aws", input{attrs: map[string]string{"log.source": "auth0"}}, "auth0"},
		{"gcp audit", "gcp", input{res: map[string]string{"cloud.resource_id": "cloudaudit.googleapis.com%2Factivity"}}, "cloudaudit.googleapis.com"},
		{"gcp cloud run", "gcp", input{res: map[string]string{"gcp.resource_type": "cloud_run_revision"}}, "run.googleapis.com"},
		{"gcp load balancer", "gcp", input{res: map[string]string{"gcp.resource_type": "http_load_balancer"}}, "loadbalancing.googleapis.com"},
		{"azure", "azure", input{}, "monitor"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, _ := tag(t, c.provider, c.in)

			wantStr(t, res, "cloud.service.name", c.want)
		})
	}
}

func TestCloudServiceName_IsAbsentWhenNoSourceIsKnown(t *testing.T) {
	for _, provider := range []string{"aws", "gcp"} {
		t.Run(provider, func(t *testing.T) {
			res, _ := tag(t, provider, input{})

			wantAbsent(t, res, "cloud.service.name")
		})
	}
}

func TestCloudServiceName_KeepsAValueTheSourceAlreadySet(t *testing.T) {
	res, _ := tag(t, "aws", input{
		res:   map[string]string{"cloud.service.name": "preset"},
		scope: map[string]string{"encoding.format": "aws.cloudtrail"},
	})

	wantStr(t, res, "cloud.service.name", "preset")
}

func TestCloudServiceName_IsAbsentWhenOneResourceMixesSources(t *testing.T) {
	ld := plog.NewLogs()
	rl := ld.ResourceLogs().AppendEmpty()
	sl := rl.ScopeLogs().AppendEmpty()
	sl.LogRecords().AppendEmpty().Attributes().PutStr("log.source", "aws.cloudtrail")
	sl.LogRecords().AppendEmpty().Attributes().PutStr("log.source", "aws.elb")

	sink := new(consumertest.LogsSink)
	consume(t, newProc(t, "aws", sink, nopSettings()), ld)

	wantAbsent(t, sink.AllLogs()[0].ResourceLogs().At(0).Resource().Attributes(), "cloud.service.name")
}

func TestCloudServiceName_UsesTheSameNameForEveryRecordOfOneSource(t *testing.T) {
	ld := plog.NewLogs()
	rl := ld.ResourceLogs().AppendEmpty()
	sl := rl.ScopeLogs().AppendEmpty()
	sl.Scope().Attributes().PutStr("encoding.format", "aws.cloudtrail")
	sl.LogRecords().AppendEmpty()
	sl.LogRecords().AppendEmpty()

	sink := new(consumertest.LogsSink)
	consume(t, newProc(t, "aws", sink, nopSettings()), ld)

	wantStr(t, sink.AllLogs()[0].ResourceLogs().At(0).Resource().Attributes(), "cloud.service.name", "cloudtrail")
}

func TestAWS_AnAuth0ResourceKeepsItsProvider(t *testing.T) {
	res, _ := tag(t, "aws", input{
		res:   map[string]string{"cloud.provider": "auth0", "cloud.region": "us-east-2", "cloud.account.id": "123456789012"},
		attrs: map[string]string{"log.source": "auth0"},
	})

	wantStr(t, res, "cloud.provider", "auth0")
	wantStr(t, res, "cloud.region", "us-east-2")
}

func TestAWS_ACloudRegionAloneDoesNotMakeTheProviderAWS(t *testing.T) {
	res, _ := tag(t, "aws", input{res: map[string]string{"cloud.region": "us-east-2"}})

	wantAbsent(t, res, "cloud.provider")
}

// Live gcp.lb rows carry zone=global and no region label.
func TestGCP_LoadBalancerRowsWithZoneGlobalGetRegionGlobal(t *testing.T) {
	res, _ := tag(t, "gcp", input{res: map[string]string{
		"gcp.resource_type":              "http_load_balancer",
		"gcp.label.zone":                 "global",
		"gcp.label.project_id":           "example-project",
		"gcp.label.backend_service_name": "example-backend",
		"gcp.label.forwarding_rule_name": "example-rule",
		"gcp.label.target_proxy_name":    "example-proxy",
		"gcp.label.url_map_name":         "example-map",
	}})

	wantStr(t, res, "cloud.region", "global")
	wantStr(t, res, "cloud.account.id", "example-project")
}

// Live gcp.audit rows without a location label: 228 of 300 sampled carry a zone, 48 a region.
func TestGCP_AuditRowsTakeTheRegionFromTheZoneOrRegionLabel(t *testing.T) {
	audit := "cloudaudit.googleapis.com%2Fdata_access"
	cases := map[string]struct {
		labels map[string]string
		want   string
	}{
		"zone":   {map[string]string{"gcp.label.zone": "europe-west3-b"}, "europe-west3"},
		"region": {map[string]string{"gcp.label.region": "europe-west3"}, "europe-west3"},
		"global": {map[string]string{"gcp.label.zone": "global"}, "global"},
	}
	for name, c := range cases {
		labels := map[string]string{"cloud.resource_id": audit, "gcp.resource_type": "gce_instance"}
		for k, v := range c.labels {
			labels[k] = v
		}
		t.Run(name, func(t *testing.T) {
			res, lr := tag(t, "gcp", input{res: labels})

			wantStr(t, res, "cloud.region", c.want)
			wantStr(t, lr.Attributes(), "log.source", "gcp.audit")
		})
	}
}

func TestGCP_AuditRowsWithNeitherLabelLeaveTheRegionUnset(t *testing.T) {
	res, _ := tag(t, "gcp", input{res: map[string]string{"cloud.resource_id": "cloudaudit.googleapis.com%2Fdata_access", "gcp.resource_type": "gce_instance"}})

	wantAbsent(t, res, "cloud.region")
}

func TestAWS_ProviderIsStillAbsentWithoutAnyRegion(t *testing.T) {
	res, _ := tag(t, "aws", input{})

	wantAbsent(t, res, "cloud.provider")
}

func TestGCP_RegionFallsBackToTheRegionLabel(t *testing.T) {
	res, _ := tag(t, "gcp", input{res: map[string]string{"gcp.resource_type": "https_lb_rule", "gcp.label.region": "us-east1"}})

	wantStr(t, res, "cloud.region", "us-east1")
}

func TestGCP_RegionFallsBackToTheZoneLabel(t *testing.T) {
	cases := map[string]string{
		"global":        "global",
		"us-central1-b": "us-central1",
		"europe-west4":  "europe-west4",
	}
	for zone, want := range cases {
		t.Run(zone, func(t *testing.T) {
			res, _ := tag(t, "gcp", input{res: map[string]string{"gcp.resource_type": "http_load_balancer", "gcp.label.zone": zone}})

			wantStr(t, res, "cloud.region", want)
		})
	}
}

func TestGCP_LocationLabelOutranksRegionAndZone(t *testing.T) {
	res, _ := tag(t, "gcp", input{res: map[string]string{
		"gcp.label.location": "us-central1", "gcp.label.region": "us-east1", "gcp.label.zone": "europe-west4-a",
	}})

	wantStr(t, res, "cloud.region", "us-central1")
}

func TestGCP_NoLocationLabelLeavesTheRegionAbsent(t *testing.T) {
	res, _ := tag(t, "gcp", input{res: map[string]string{"gcp.resource_type": "http_load_balancer"}})

	wantAbsent(t, res, "cloud.region")
}

func TestGCP_RecordsWithoutSeverityGetInfo(t *testing.T) {
	for name, res := range map[string]map[string]string{
		"audit":     {"cloud.resource_id": "cloudaudit.googleapis.com%2Factivity"},
		"cloud run": {"gcp.resource_type": "cloud_run_revision"},
		"lb":        {"gcp.resource_type": "http_load_balancer"},
		"untagged":  {},
	} {
		t.Run(name, func(t *testing.T) {
			_, lr := tag(t, "gcp", input{res: res})

			if lr.SeverityNumber() != plog.SeverityNumberInfo || lr.SeverityText() != "INFO" {
				t.Errorf("severity = %v/%q, want INFO/INFO", lr.SeverityNumber(), lr.SeverityText())
			}
		})
	}
}

func TestGCP_ASeverityTheEntryCarriesIsKept(t *testing.T) {
	_, lr := tag(t, "gcp", input{severity: plog.SeverityNumberError, severityText: "ERROR"})

	if lr.SeverityNumber() != plog.SeverityNumberError || lr.SeverityText() != "ERROR" {
		t.Errorf("severity = %v/%q, want ERROR/ERROR", lr.SeverityNumber(), lr.SeverityText())
	}
}

func TestGCP_ANoticeKeepsItsOwnText(t *testing.T) {
	_, lr := tag(t, "gcp", input{severity: plog.SeverityNumberInfo2, severityText: "NOTICE"})

	if lr.SeverityNumber() != plog.SeverityNumberInfo2 || lr.SeverityText() != "NOTICE" {
		t.Errorf("severity = %v/%q, want INFO2/NOTICE", lr.SeverityNumber(), lr.SeverityText())
	}
}

func TestAzure_RecordsWithoutSeverityGetInfoAndASetOneIsKept(t *testing.T) {
	_, lr := tag(t, "azure", input{})
	if lr.SeverityNumber() != plog.SeverityNumberInfo || lr.SeverityText() != "INFO" {
		t.Errorf("severity = %v/%q, want INFO/INFO", lr.SeverityNumber(), lr.SeverityText())
	}

	_, lr = tag(t, "azure", input{severity: plog.SeverityNumberWarn, severityText: "Warning"})
	if lr.SeverityNumber() != plog.SeverityNumberWarn || lr.SeverityText() != "Warning" {
		t.Errorf("severity = %v/%q, want WARN/Warning", lr.SeverityNumber(), lr.SeverityText())
	}
}
