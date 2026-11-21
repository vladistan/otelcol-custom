// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

// cspell:ignore awselb

import (
	"slices"
	"testing"
)

// claimFixtures holds one representative record per format. owner is the only branch allowed to claim it.
func claimFixtures() []struct {
	name  string
	owner string
	in    input
} {
	cloudAttrs := func(source string) map[string]any {
		return map[string]any{"log.source": source}
	}
	return []struct {
		name  string
		owner string
		in    input
	}{
		{"influx", "influx", input{body: `[httpd] 192.0.2.19 - webuser [20/Nov/2026:00:02:45 +0100] "POST /write?db=zero HTTP/1.1" 204 0 "-" "InfluxDBClient" 5a1b2c3d-1111-2222-3333-444455556666 14684`}},
		{"oauth2proxy", "oauth2proxy", input{body: `192.0.2.23 - 7f1e2d3c-aaaa-bbbb-cccc-ddddeeeeffff - - [2026/11/20 04:42:11] 192.0.2.46:4180 GET - "/ready" HTTP/1.1 "kube-probe/1.36" 200 2 0.000`}},
		{"coredns", "coredns", input{body: `[INFO] 192.0.2.42:56818 - 13677 "A IN nohost. udp 24 false 512" SERVFAIL qr,aa,rd,ra 24 0.000070318s`}},
		{"gcplb", "gcplb", input{body: "request", attrs: cloudAttrs("gcp.lb")}},
		{"awselb", "awselb", input{body: "request", attrs: cloudAttrs("aws.elb")}},
		{"s3access", "s3access", input{body: "request", attrs: cloudAttrs("aws.s3access")}},
		{"gcpaudit", "gcpaudit", input{body: "audit", attrs: cloudAttrs("gcp.audit")}},
		{"eksaudit", "eksaudit", input{
			body:  `{"kind":"Event","level":"Metadata","auditID":"11111111-2222-3333-4444-555555555555","verb":"get","user":{"username":"system:anonymous"},"objectRef":{"resource":"leases","namespace":"kube-system","name":"x"},"responseStatus":{"code":200}}`,
			attrs: cloudAttrs("aws.eks"),
		}},
		{"cloudtrail", "cloudtrail", input{body: "ssm.amazonaws.com GetParameter", attrs: cloudAttrs("aws.cloudtrail")}},
		{"k8s pod status", "k8s", input{
			body:     "Pod team-a/job-1-abcde: Succeeded (0/1 ready) on node-a",
			attrs:    map[string]any{"event.domain": "k8s", "k8s.pod.name": "job-1-abcde", "k8s.namespace.name": "team-a"},
			resource: map[string]any{"k8s.cluster.name": "cluster-1"},
		}},
		{"k8s event json", "k8s", input{
			body: `{"apiVersion":"v1","involvedObject":{"kind":"RedirectService","name":"default","namespace":"kube-system"},` +
				`"kind":"Event","message":"deleted redirect service obj default","metadata":{"name":"default.18e952fe67152ff9","namespace":"kube-system"},` +
				`"reason":"UpdateRedirectService","source":{"component":"redirect-service-controller","host":"node-a"},"type":"Normal"}`,
		}},
	}
}

func TestClaims_W2Fixtures_ExactlyOneBranchClaims(t *testing.T) {
	branches, err := newBranches(defaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	for _, fx := range claimFixtures() {
		t.Run(fx.name, func(t *testing.T) {
			rl := fx.in.logs().ResourceLogs().At(0)
			sl := rl.ScopeLogs().At(0)
			lr := sl.LogRecords().At(0)
			var claimed []string
			for _, b := range branches {
				if b.claims(&rec{lr: lr, res: rl.Resource(), scope: sl.Scope(), body: bodyText(lr)}) {
					claimed = append(claimed, b.name())
				}
			}
			if !slices.Equal(claimed, []string{fx.owner}) {
				t.Fatalf("claimed by %v, want only %q", claimed, fx.owner)
			}
		})
	}
}
