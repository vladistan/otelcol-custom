// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"testing"

	"go.opentelemetry.io/collector/pdata/plog"
)

// A line of a load balancer or a bucket gives the same Body and severity whichever account or
// project it comes from and whichever alias list the configuration holds: only the resource
// (host, service, account name) follows the account.
func TestConsume_SameLineFromTwoAccounts_GivesTheSameBodyAndSeverity(t *testing.T) {
	second := defaultConfig()
	second.AccountAliases = map[string]string{"444455556666": "second-account", "second-project": "second-project-alias"}
	second.GCPLBErrorValues = []string{"some_extra_detail"}

	cases := []struct {
		name       string
		attrs      map[string]any
		lab, other map[string]any
		wantBody   string
	}{
		{
			name:  "elb",
			attrs: awselbAttrs(nil),
			lab:   awselbResource(),
			other: map[string]any{"cloud.resource_id": "app/second-lb/0123456789abcdef", "cloud.account.id": "444455556666", "cloud.region": "eu-west-1"},
			// The body has no account, load balancer name or region in it.
			wantBody: "GET os.app.example.invalid/api/v1/bus/status 200 11ms",
		},
		{
			name:     "s3access",
			attrs:    s3accessAttrs(nil),
			lab:      s3accessResource(),
			other:    map[string]any{"aws.s3.bucket": "bucket-name", "cloud.account.id": "444455556666"},
			wantBody: "BATCH.DELETE.OBJECT bucket-name/.chunks/ab/abcdef 204 174KiB",
		},
		{
			name:     "s3access without an account id",
			attrs:    s3accessAttrs(nil),
			lab:      s3accessResource(),
			other:    map[string]any{"aws.s3.bucket": "bucket-name"},
			wantBody: "BATCH.DELETE.OBJECT bucket-name/.chunks/ab/abcdef 204 174KiB",
		},
		{
			name:     "lb",
			attrs:    gcplbAttrs(nil),
			lab:      gcplbResource(),
			other:    map[string]any{"gcp.label.project_id": "second-project", "gcp.label.backend_service_name": "k8s1-0a1b2c3d-monitoring-grafana-3000-4e5f6a7b"},
			wantBody: "GET grafana.admin.example.invalid/api/live/ws 101 40s",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			labRec, _ := awselbRun(t, awselbConfig(), input{attrs: c.attrs, resource: c.lab})
			otherRec, _ := awselbRun(t, second, input{attrs: c.attrs, resource: c.other})

			if labRec.Body().Str() != c.wantBody || otherRec.Body().Str() != c.wantBody {
				t.Fatalf("bodies = %q and %q, want %q for both", labRec.Body().Str(), otherRec.Body().Str(), c.wantBody)
			}
			if labRec.SeverityNumber() != otherRec.SeverityNumber() || labRec.SeverityText() != otherRec.SeverityText() {
				t.Fatalf("severity differs: %v %q and %v %q", labRec.SeverityNumber(), labRec.SeverityText(), otherRec.SeverityNumber(), otherRec.SeverityText())
			}
		})
	}
}

// The resource follows the account: the same line from two accounts gets two different host names
// where an alias is set for one of them.
func TestConsume_SameLineFromTwoAccounts_OnlyTheResourceDiffers(t *testing.T) {
	cfg := defaultConfig()
	cfg.AccountAliases = map[string]string{"111122223333": "first-account", "444455556666": "second-account"}
	other := map[string]any{"cloud.resource_id": "app/second-lb/0123456789abcdef", "cloud.account.id": "444455556666"}

	_, first := awselbRun(t, cfg, input{attrs: awselbAttrs(nil), resource: awselbResource()})
	lr, second := awselbRun(t, cfg, input{attrs: awselbAttrs(nil), resource: other})

	if first["cloud.account.name"] != "first-account" || second["cloud.account.name"] != "second-account" {
		t.Fatalf("account names = %v and %v", first["cloud.account.name"], second["cloud.account.name"])
	}
	if first["host.name"] == second["host.name"] {
		t.Fatalf("both accounts got the host %v", first["host.name"])
	}
	if lr.SeverityNumber() != plog.SeverityNumberInfo {
		t.Fatalf("severity = %v", lr.SeverityNumber())
	}
}
