// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import "testing"

// accountNameCases cover a different alias, an identity alias, no alias and no project.
var accountNameCases = []struct {
	name     string
	project  string
	aliases  map[string]string
	wantName string
	wantHost string
}{
	{
		name:     "different alias",
		project:  "project-a",
		aliases:  map[string]string{"project-a": "team-a"},
		wantName: "team-a",
		wantHost: "team-a",
	},
	{
		name:     "identity alias",
		project:  "project-b",
		aliases:  map[string]string{"project-b": "project-b"},
		wantName: "project-b",
		wantHost: "project-b",
	},
	{
		name:     "no alias falls back to the project id",
		project:  "project-c",
		aliases:  map[string]string{"project-a": "team-a"},
		wantName: "project-c",
		wantHost: "project-c",
	},
	{
		name:     "no alias map at all",
		project:  "project-c",
		aliases:  nil,
		wantName: "project-c",
		wantHost: "project-c",
	},
	{
		name:     "no project id",
		project:  "",
		aliases:  map[string]string{"project-a": "team-a"},
		wantName: "",
		wantHost: "",
	},
}

func TestConsume_GCPLB_AccountName_ForEveryProject(t *testing.T) {
	for _, tc := range accountNameCases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := gcplbOnly(nil)
			cfg.AccountAliases = tc.aliases
			res := gcplbResource()
			if tc.project == "" {
				delete(res, "gcp.label.project_id")
			} else {
				res["gcp.label.project_id"] = tc.project
			}
			_, got := gcplbRun(t, cfg, input{attrs: gcplbAttrs(nil), resource: res})

			if tc.wantHost == "" {
				absent(t, mapOf(t, got), "host.name", "cloud.account.name")
				return
			}
			if got["host.name"] != tc.wantHost {
				t.Fatalf("host.name = %v, want %q", got["host.name"], tc.wantHost)
			}
			if got["cloud.account.name"] != tc.wantName {
				t.Fatalf("cloud.account.name = %v, want %q", got["cloud.account.name"], tc.wantName)
			}
		})
	}
}

func TestConsume_GCPAudit_AccountName_ForEveryProject(t *testing.T) {
	for _, tc := range accountNameCases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := defaultConfig()
			cfg.Branches = []string{"gcpaudit"}
			cfg.AccountAliases = tc.aliases
			in := gcpAuditInput(gcpAuditK8sAttrs("a@example.invalid", nil), "pods.get")
			if tc.project == "" {
				delete(in.resource, "gcp.label.project_id")
			} else {
				in.resource["gcp.label.project_id"] = tc.project
			}
			_, res := newHarness(t, cfg).run(in)

			if tc.wantName == "" {
				absent(t, res.Attributes(), "cloud.account.name")
				return
			}
			if got := attrStr(t, res.Attributes(), "cloud.account.name"); got != tc.wantName {
				t.Fatalf("cloud.account.name = %q, want %q", got, tc.wantName)
			}
		})
	}
}

func TestConsume_GCPLB_AccountName_MatchesGCPAudit(t *testing.T) {
	for _, tc := range accountNameCases {
		t.Run(tc.name, func(t *testing.T) {
			lbCfg := gcplbOnly(nil)
			lbCfg.AccountAliases = tc.aliases
			lbRes := gcplbResource()
			auditCfg := defaultConfig()
			auditCfg.Branches = []string{"gcpaudit"}
			auditCfg.AccountAliases = tc.aliases
			in := gcpAuditInput(gcpAuditK8sAttrs("a@example.invalid", nil), "pods.get")
			if tc.project == "" {
				delete(lbRes, "gcp.label.project_id")
				delete(in.resource, "gcp.label.project_id")
			} else {
				lbRes["gcp.label.project_id"] = tc.project
				in.resource["gcp.label.project_id"] = tc.project
			}
			_, lb := gcplbRun(t, lbCfg, input{attrs: gcplbAttrs(nil), resource: lbRes})
			_, auditRes := newHarness(t, auditCfg).run(in)
			audit := auditRes.Attributes().AsRaw()

			lbName, lbHas := lb["cloud.account.name"]
			auditName, auditHas := audit["cloud.account.name"]
			if lbHas != auditHas || lbName != auditName {
				t.Fatalf("gcplb name = %v (set %v), gcpaudit name = %v (set %v)", lbName, lbHas, auditName, auditHas)
			}
		})
	}
}

func TestConsume_AWSBranches_AccountWithoutAlias_GetsNoAccountName(t *testing.T) {
	t.Run("awselb", func(t *testing.T) {
		res := map[string]any{"cloud.resource_id": "app/example-lb/50dc6c495c0c9188", "cloud.account.id": "444455556666"}
		_, got := awselbRun(t, awselbConfig(), input{attrs: awselbAttrs(nil), resource: res})
		absent(t, mapOf(t, got), "cloud.account.name")
	})
	t.Run("s3access", func(t *testing.T) {
		res := s3accessResource()
		res["cloud.account.id"] = "444455556666"
		_, got := s3accessRun(t, s3accessConfig(), input{attrs: s3accessAttrs(nil), resource: res})
		absent(t, mapOf(t, got), "cloud.account.name")
	})
	t.Run("cloudtrail", func(t *testing.T) {
		_, res := newHarness(t, defaultConfig()).run(cloudTrailInput(cloudTrailRoleAttrs()))
		absent(t, res.Attributes(), "cloud.account.name")
	})
}
