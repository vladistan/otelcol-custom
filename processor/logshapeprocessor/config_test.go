// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

// cspell:ignore awselb

import (
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/collector/pdata/plog"
)

func TestValidate_UnknownBranchName_Fails(t *testing.T) {
	cfg := defaultConfig()
	cfg.Branches = []string{"klog", "nope"}

	err := cfg.Validate()

	if err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("Validate = %v, want an error naming nope", err)
	}
}

func TestValidate_KnownBranchNames_Pass(t *testing.T) {
	cfg := defaultConfig()
	cfg.Branches = []string{"klog", "uvicorn"}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate = %v", err)
	}
}

func TestValidate_UnknownLevelInAnOverride_Fails(t *testing.T) {
	cfg := defaultConfig()
	cfg.Klog.LevelOverrides = map[string]string{"W": "loud"}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "loud") {
		t.Fatalf("Validate = %v, want an error naming loud", err)
	}
}

func TestDefaultConfig_EnablesEveryBranch(t *testing.T) {
	got := defaultConfig().Branches
	for _, want := range []string{
		"klog", "uvicorn", "influx", "oauth2proxy", "coredns", "gcplb",
		"awselb", "s3access", "gcpaudit", "eksaudit", "cloudtrail", "k8s",
	} {
		found := false
		for _, b := range got {
			found = found || b == want
		}
		if !found {
			t.Errorf("default branches %v lack %s", got, want)
		}
	}
}

func TestDefaultConfig_NewKeysAreEmptyAndValid(t *testing.T) {
	cfg := defaultConfig()

	if len(cfg.AccountAliases) != 0 || len(cfg.GCPLBErrorValues) != 0 ||
		len(cfg.K8s.StaticPodNodes) != 0 || len(cfg.ServiceNameOverrides) != 0 {
		t.Fatalf("new keys not empty by default: %+v", cfg)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate = %v", err)
	}
}

func TestValidate_EmptyAliasValue_NamesTheKey(t *testing.T) {
	cfg := defaultConfig()
	cfg.AccountAliases = map[string]string{"acct-1": ""}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "acct-1") {
		t.Fatalf("Validate = %v, want an error naming acct-1", err)
	}
}

func TestValidate_EmptyAliasKey_Fails(t *testing.T) {
	cfg := defaultConfig()
	cfg.AccountAliases = map[string]string{"": "prod"}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "account_aliases") {
		t.Fatalf("Validate = %v, want an account_aliases error", err)
	}
}

func TestValidate_EmptyGCPLBErrorValue_NamesTheKey(t *testing.T) {
	cfg := defaultConfig()
	cfg.GCPLBErrorValues = []string{"extra", ""}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "gcp_lb_error_values") {
		t.Fatalf("Validate = %v, want a gcp_lb_error_values error", err)
	}
}

func TestGCPLBErrorValues_ListExtendsTheCodeDefault(t *testing.T) {
	got := gcpLBErrorValues([]string{"custom_value", "unsupported_method"})

	want := []string{"unsupported_method", "invalid_http2_request_content_length", "custom_value"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("got %v, want %v", got, want)
	}
	if len(gcpLBErrorValues(nil)) != 2 {
		t.Fatalf("defaults lost: %v", gcpLBErrorValues(nil))
	}
}

func TestOverride_Scalar_ReplacesTheDefault(t *testing.T) {
	cfg := defaultConfig()
	cfg.Klog.Year = 2025
	h := newHarness(t, cfg)

	lr, _ := h.run(input{body: klogLine, ts: recordTime})

	if y := lr.Timestamp().AsTime().Year(); y != 2025 {
		t.Fatalf("year = %d, want 2025", y)
	}
}

func TestOverride_List_ExtendsTheDefault(t *testing.T) {
	cfg := defaultConfig()
	cfg.Klog.StaticPodPrefixes = []string{"kube-vip-"}
	h := newHarness(t, cfg)

	_, res := h.run(input{body: klogLine, ts: recordTime, resource: map[string]any{"k8s.pod.name": "kube-vip-k8s-9"}})
	if attrStr(t, res.Attributes(), "host.name") != "k8s-9" {
		t.Fatalf("configured prefix not used: %v", res.Attributes().AsRaw())
	}
	_, res = h.run(input{body: klogLine, ts: recordTime, resource: map[string]any{"k8s.pod.name": "etcd-k8s-3"}})
	if attrStr(t, res.Attributes(), "host.name") != "k8s-3" {
		t.Fatalf("default prefix lost: %v", res.Attributes().AsRaw())
	}
}

func TestOverride_Map_ReplacesOneKeyAndKeepsTheRest(t *testing.T) {
	cfg := defaultConfig()
	cfg.Klog.LevelOverrides = map[string]string{"W": "error"}
	h := newHarness(t, cfg)
	const warn = `W1119 23:05:39.184859       1 client.go:214] "slow" n=1`

	lr, _ := h.run(input{body: warn, ts: recordTime})
	if lr.SeverityNumber() != plog.SeverityNumberError || lr.SeverityText() != "ERROR" {
		t.Fatalf("W gave %d %q, want ERROR", lr.SeverityNumber(), lr.SeverityText())
	}
	lr, _ = h.run(input{body: klogLine, ts: recordTime})
	if lr.SeverityNumber() != plog.SeverityNumberInfo {
		t.Fatalf("I gave %d, want INFO", lr.SeverityNumber())
	}
}

func TestSeverity_TextAndNumberAlwaysAgree(t *testing.T) {
	h := newHarness(t, defaultConfig())
	for _, c := range []struct {
		line string
		num  plog.SeverityNumber
		text string
	}{
		{`I1119 23:05:39.184859       1 a.go:1] "m"`, plog.SeverityNumberInfo, "INFO"},
		{`W1119 23:05:39.184859       1 a.go:1] "m"`, plog.SeverityNumberWarn, "WARN"},
		{`E1119 23:05:39.184859       1 a.go:1] "m"`, plog.SeverityNumberError, "ERROR"},
		{`F1119 23:05:39.184859       1 a.go:1] "m"`, plog.SeverityNumberFatal, "FATAL"},
	} {
		lr, _ := h.run(input{body: c.line, ts: time.Date(2026, 11, 19, 23, 6, 0, 0, time.UTC)})
		if lr.SeverityNumber() != c.num || lr.SeverityText() != c.text {
			t.Errorf("%q: %d %q, want %d %q", c.line, lr.SeverityNumber(), lr.SeverityText(), c.num, c.text)
		}
	}
}

func TestValidate_ServiceNameOverrides_EmptyKeyOrValue_Rejected(t *testing.T) {
	for _, m := range []map[string]string{{"": "x"}, {"x": ""}} {
		cfg := defaultConfig()
		cfg.ServiceNameOverrides = m

		err := cfg.Validate()

		if err == nil || !strings.Contains(err.Error(), "service_name_overrides") {
			t.Fatalf("overrides %v: err = %v, want one naming service_name_overrides", m, err)
		}
	}
}
