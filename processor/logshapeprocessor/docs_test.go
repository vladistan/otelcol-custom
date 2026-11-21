// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vladistan/otelcol-custom/shape/rename"
)

func readDoc(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "log-shaping.md"))
	if err != nil {
		t.Fatalf("docs/log-shaping.md: %v", err)
	}
	return string(raw)
}

func TestDocs_NameEveryBranch(t *testing.T) {
	doc := readDoc(t)
	for _, name := range branchNames {
		if !strings.Contains(doc, "| `"+name+"` |") {
			t.Errorf("the branch table has no row for %s", name)
		}
	}
}

func TestDocs_ListEveryRenameOfTheTable(t *testing.T) {
	doc := readDoc(t)
	for _, r := range rename.Table {
		if !strings.Contains(doc, "`"+r.From+"`") {
			t.Errorf("the rename %s to %s is not in the docs", r.From, r.To)
		}
	}
}

func TestDocs_ListTheIntegerAttributesAndTheRetentionKeys(t *testing.T) {
	doc := readDoc(t)
	for _, k := range append(append([]string{}, rename.IntKeys...), "log.source", "flow.type", "http.request.method", "SeverityNumber", "Namespace") {
		if !strings.Contains(doc, k) {
			t.Errorf("%s is not in the docs", k)
		}
	}
}

func TestDocs_StorageSectionHasTheTypedColumnsTheTablesAndTheDDLOwners(t *testing.T) {
	doc := readDoc(t)
	_, storage, found := strings.Cut(doc, "## Storage")
	if !found {
		t.Fatal("no Storage section")
	}
	storage, _, _ = strings.Cut(storage, "\n## ")
	for _, want := range []string{"HostName", "Namespace", "PodName", "`otel.otel_logs`", "`default.otel_logs`", "DDL owner", "string map", "does not change"} {
		if !strings.Contains(storage, want) {
			t.Errorf("the Storage section lacks %q", want)
		}
	}
}

func TestDocs_RegistrySectionHoldsTheResultNotAPlaceholder(t *testing.T) {
	doc := readDoc(t)
	if strings.Contains(doc, "REGISTRY_RESULT") {
		t.Fatal("the registry section is still a placeholder")
	}
	_, section, found := strings.Cut(doc, "## Registry check")
	if !found {
		t.Fatal("no Registry check section")
	}
	section, _, _ = strings.Cut(section, "\n## ")
	for _, want := range []string{"user_agent.original", "rpc.status_code", "Influx", "verified", "memory"} {
		if !strings.Contains(section, want) {
			t.Errorf("the registry section lacks %q", want)
		}
	}
}
