// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"os"
	"slices"
	"strings"
	"testing"

	"go.opentelemetry.io/collector/pdata/plog"
)

const lineService = "example-sync"

// lineConfig lists the example service for severity_from_line and claims no line by shape.
func lineConfig(services ...string) *Config {
	cfg := defaultConfig()
	cfg.Branches = nil
	cfg.SeverityFromLine.Services = services
	return cfg
}

// lineRecord is one record of the service with the given body and severity number.
func lineRecord(service, body string, sev plog.SeverityNumber, attrs map[string]any) plog.Logs {
	ld := plog.NewLogs()
	rl := ld.ResourceLogs().AppendEmpty()
	rl.Resource().Attributes().PutStr("service.name", service)
	lr := rl.ScopeLogs().AppendEmpty().LogRecords().AppendEmpty()
	lr.Body().SetStr(body)
	lr.SetSeverityNumber(sev)
	if sev != plog.SeverityNumberUnspecified {
		lr.SetSeverityText(sev.String())
	}
	if err := lr.Attributes().FromRaw(attrs); err != nil {
		panic(err)
	}
	return ld
}

func levelOf(t *testing.T, h *harness, ld plog.Logs) (plog.SeverityNumber, string) {
	t.Helper()
	out := flatten(h.runBatch(ld))
	if len(out) != 1 {
		t.Fatalf("%d records out, want 1", len(out))
	}
	return out[0].severity, out[0].severityText
}

func TestSeverityFromLine_LevelTokenGivesTheSeverityOfARecordWithNone(t *testing.T) {
	for _, tt := range []struct {
		name, body string
		num        plog.SeverityNumber
		text       string
	}{
		{"key and value", `level=warn msg="retrying"`, plog.SeverityNumberWarn, "WARN"},
		{"quoted value", `lvl="Warning" msg=x`, plog.SeverityNumberWarn, "WARN"},
		{"severity key", `severity=fatal msg=x`, plog.SeverityNumberFatal, "FATAL"},
		{"brackets", `[ERROR] sync failed`, plog.SeverityNumberError, "ERROR"},
		{"brackets after text", `sync run 4 [debug] tick`, plog.SeverityNumberDebug, "DEBUG"},
		{"parentheses", `(crit) disk gone`, plog.SeverityNumberFatal, "FATAL"},
		{"leading word", `INFO sync batch complete`, plog.SeverityNumberInfo, "INFO"},
		{"leading word with colon", `error: sync failed`, plog.SeverityNumberError, "ERROR"},
		{"leading word lower case", `trace entering sync`, plog.SeverityNumberTrace, "TRACE"},
		{"after a timestamp", `2026-01-02T03:04:05Z debug heartbeat`, plog.SeverityNumberDebug, "DEBUG"},
		{"after a timestamp with zone", `2026-01-02 03:04:05.123+01:00 NOTICE started`, plog.SeverityNumberInfo, "INFO"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, lineConfig(lineService))

			num, text := levelOf(t, h, lineRecord(lineService, tt.body, plog.SeverityNumberUnspecified, nil))

			if num != tt.num || text != tt.text {
				t.Errorf("severity = %d %q, want %d %q", num, text, tt.num, tt.text)
			}
		})
	}
}

func TestSeverityFromLine_NoTokenOrAnUnknownTokenStaysUnset(t *testing.T) {
	for _, tt := range []struct{ name, body string }{
		{"no token", `sync idle`},
		{"empty line", ``},
		{"word in free text", `the sync saw an error in the queue`},
		{"word after a plain word", `reconciler error: sync failed`},
		{"unknown value", `level=chatty msg="not a level"`},
		{"unknown bracket", `[verbose] sync idle`},
		{"unknown leading word", `Started sync`},
		{"key without a value", `level= msg=x`},
		{"word inside another word", `errors seen: 0`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, lineConfig(lineService))

			num, text := levelOf(t, h, lineRecord(lineService, tt.body, plog.SeverityNumberUnspecified, nil))

			if num != plog.SeverityNumberUnspecified || text != "" {
				t.Errorf("severity = %d %q, want unset", num, text)
			}
		})
	}
}

func TestSeverityFromLine_ASeverityAlreadySetIsKept(t *testing.T) {
	for _, sev := range []plog.SeverityNumber{plog.SeverityNumberInfo, plog.SeverityNumberError, plog.SeverityNumberTrace2} {
		h := newHarness(t, lineConfig(lineService))

		num, text := levelOf(t, h, lineRecord(lineService, `level=fatal boom`, sev, nil))

		if num != sev || text != sev.String() {
			t.Errorf("severity %v became %d %q, want it kept", sev, num, text)
		}
	}
}

func TestSeverityFromLine_AnotherServiceOrNoConfigIsUntouched(t *testing.T) {
	for _, tt := range []struct {
		name     string
		services []string
		service  string
	}{
		{"service not listed", []string{lineService}, "other-service"},
		{"no service name", []string{lineService}, ""},
		{"empty list", nil, lineService},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, lineConfig(tt.services...))

			num, text := levelOf(t, h, lineRecord(tt.service, `level=error boom`, plog.SeverityNumberUnspecified, nil))

			if num != plog.SeverityNumberUnspecified || text != "" {
				t.Errorf("severity = %d %q, want unset", num, text)
			}
		})
	}
}

func TestSeverityFromLine_ReadsTheOriginalLineWhenABranchRewroteTheBody(t *testing.T) {
	h := newHarness(t, lineConfig(lineService))
	attrs := map[string]any{"log.record.original": `level=error sync failed`}

	num, text := levelOf(t, h, lineRecord(lineService, `sync failed`, plog.SeverityNumberUnspecified, attrs))

	if num != plog.SeverityNumberError || text != "ERROR" {
		t.Errorf("severity = %d %q, want ERROR from the original line", num, text)
	}
}

func TestSeverityFromLine_WorksOnARecordABranchClaimedAndChangesNothingElse(t *testing.T) {
	cfg := defaultConfig()
	cfg.Branches = []string{"autoscaler"}
	cfg.SeverityFromLine.Services = []string{lineService}
	h := newHarness(t, cfg)
	row := `{"status":{"autoscaledNodesCount":3,"autoscaledNodesTarget":5,"note":"[WARN] slow scale up"}}`
	plain := newHarness(t, func() *Config { c := defaultConfig(); c.Branches = []string{"autoscaler"}; return c }())

	got := flatten(h.runBatch(lineRecord(lineService, row, plog.SeverityNumberUnspecified, nil)))
	want := flatten(plain.runBatch(lineRecord(lineService, row, plog.SeverityNumberUnspecified, nil)))

	if len(got) != 1 || len(want) != 1 {
		t.Fatalf("%d and %d records out, want 1 each", len(got), len(want))
	}
	if got[0].severity != plog.SeverityNumberWarn || got[0].severityText != "WARN" {
		t.Errorf("severity = %d %q, want WARN", got[0].severity, got[0].severityText)
	}
	if got[0].body != want[0].body || got[0].service != want[0].service {
		t.Errorf("body or service changed: %q/%q, want %q/%q", got[0].body, got[0].service, want[0].body, want[0].service)
	}
	if len(got[0].attrs) != len(want[0].attrs) {
		t.Errorf("attributes = %v, want %v", got[0].attrs, want[0].attrs)
	}
	for k, v := range want[0].attrs {
		if got[0].attrs[k] != v {
			t.Errorf("attribute %s = %v, want %v", k, got[0].attrs[k], v)
		}
	}
}

func TestSeverityFromLine_DefaultListIsEmpty(t *testing.T) {
	if got := defaultConfig().SeverityFromLine.Services; len(got) != 0 {
		t.Fatalf("default services = %v, want none", got)
	}
}

func TestSeverityFromLine_AnEmptyServiceEntryFailsValidate(t *testing.T) {
	cfg := defaultConfig()
	cfg.SeverityFromLine.Services = []string{lineService, ""}

	err := cfg.Validate()

	if err == nil || !strings.Contains(err.Error(), "severity_from_line.services") {
		t.Fatalf("Validate() = %v, want an error naming severity_from_line.services", err)
	}
}

func TestSeverityFromLine_RealContainerLinesWithNoLevelTokenStayUnset(t *testing.T) {
	raw, err := os.ReadFile("testdata/severity-from-line/example-sync.txt")
	if err != nil {
		t.Fatal(err)
	}
	f, err := parseSampleFile(raw)
	if err != nil {
		t.Fatal(err)
	}
	if f.provenance != "partly real" {
		t.Fatalf("provenance = %q, want partly real", f.provenance)
	}
	if want := []int{1, 2, 3, 4, 5, 6, 7, 8, 10}; !slices.Equal(f.realLines, want) {
		t.Fatalf("real lines = %v, want %v", f.realLines, want)
	}
	if len(f.lines) != 10 {
		t.Fatalf("%d sample lines, want 10", len(f.lines))
	}
	listed := newHarness(t, lineConfig(lineService))
	unlisted := newHarness(t, lineConfig())
	for i, l := range f.lines {
		got := flatten(listed.runBatch(lineRecord(lineService, l, plog.SeverityNumberUnspecified, nil)))
		want := flatten(unlisted.runBatch(lineRecord(lineService, l, plog.SeverityNumberUnspecified, nil)))
		if len(got) != 1 || len(want) != 1 {
			t.Fatalf("line %d: %d and %d records out, want 1 each", i+1, len(got), len(want))
		}
		if got[0].severity != plog.SeverityNumberUnspecified || got[0].severityText != "" {
			t.Errorf("line %d %q: severity = %d %q, want unset", i+1, l, got[0].severity, got[0].severityText)
		}
		if got[0].body != l {
			t.Errorf("line %d: body = %q, want it unchanged", i+1, got[0].body)
		}
		if got[0].severity != want[0].severity || got[0].severityText != want[0].severityText || got[0].body != want[0].body {
			t.Errorf("line %d: the listed service gave %+v, an unlisted one %+v", i+1, got[0], want[0])
		}
	}
}
