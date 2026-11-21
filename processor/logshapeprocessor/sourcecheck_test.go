// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// deprecatedName finds a quoted deprecated attribute name. The names are the ones components of this
// repo must stop writing: the successors are deployment.environment.name, rpc.system.name, rpc.method,
// rpc.status_code, the EventName field, code.file.path, code.line.number and code.function.name.
var deprecatedName = regexp.MustCompile(`"(deployment\.environment|rpc\.system|rpc\.service|rpc\.response\.status_code|event\.name|code\.filepath|code\.lineno|code\.function)"`)

// stillWriting are the lab components that logshape replaces. They stay in the manifests until their
// branch runs in production, so they keep writing the names they always wrote. Each entry goes when its
// component is removed.
var stillWriting = map[string]string{
	"processor/ciscologprocessor":  "code.filepath; the cisco branch of logshape writes code.file.path",
	"processor/klogprocessor":      "code.filepath; the klog branch of logshape writes code.file.path",
	"processor/logruslogprocessor": "code.filepath; the logrus branch of logshape writes code.file.path",
}

// readOnly lists the places that mention a deprecated name only to read it. Writing such a name is
// forbidden everywhere. Reading one is allowed when an upstream encoder still emits it, and each entry
// says which one. The key is the file path as the scan reports it, then the quoted literal.
var readOnly = map[string]map[string]string{
	"processor/logshapeprocessor/cloudtrail.go": {
		"rpc.service": "awslogsencodingextension v0.164.1 cloudtraillog/unmarshaler.go emits rpc.service (and rpc.system) by default; it is the only carrier of the event source unless EmitV1RPCConventions is on",
	},
	"processor/logshapeprocessor/gcpaudit.go": {
		"rpc.response.status_code": "googlecloudlogentryencodingextension v0.164.1 internal/auditlog/parser.go emits rpc.response.status_code (as a string) on audit records",
	},
}

// deprecatedHits returns every deprecated literal in src that the read-only table does not allow for rel.
func deprecatedHits(rel, src string) []string {
	var hits []string
	for _, m := range deprecatedName.FindAllString(src, -1) {
		if _, ok := readOnly[rel][strings.Trim(m, `"`)]; !ok {
			hits = append(hits, rel+" "+m)
		}
	}
	return hits
}

func moduleOf(dir string, cache map[string]string) string {
	if m, ok := cache[dir]; ok {
		return m
	}
	m := ""
	if raw, err := os.ReadFile(filepath.Join(dir, "go.mod")); err == nil {
		first, _, _ := strings.Cut(string(raw), "\n")
		m = strings.TrimSpace(strings.TrimPrefix(first, "module"))
	} else if parent := filepath.Dir(dir); parent != dir {
		m = moduleOf(parent, cache)
	}
	cache[dir] = m
	return m
}

func TestSource_NoComponentOfThisRepoWritesADeprecatedName(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	cache := map[string]string{}
	var found []string
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		if d.IsDir() {
			switch d.Name() {
			case ".git", "vendor", "node_modules", "testdata", "dist":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || strings.HasPrefix(rel, "shape/rename/") {
			return nil
		}
		if m := moduleOf(filepath.Dir(path), cache); strings.HasPrefix(m, "github.com/open-telemetry/") || strings.HasPrefix(m, "go.opentelemetry.io/") {
			return nil // upstream text is not ours to change
		}
		for dir := range stillWriting {
			if strings.HasPrefix(rel, dir+"/") {
				return nil
			}
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		found = append(found, deprecatedHits(rel, string(raw))...)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range found {
		t.Errorf("writes a deprecated name: %s", f)
	}
}

func TestSource_EveryAllowedComponentStillExists(t *testing.T) {
	for dir := range stillWriting {
		if _, err := os.Stat(filepath.Join("..", "..", dir)); err != nil {
			t.Errorf("%s is on the allow list but is gone: remove the entry", dir)
		}
	}
}

func TestSource_EveryReadOnlyEntryIsStillUsed(t *testing.T) {
	root := filepath.Join("..", "..")
	for rel, lits := range readOnly {
		raw, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Errorf("%s is on the read-only list but cannot be read (%v): remove the entry", rel, err)
			continue
		}
		for lit := range lits {
			if !strings.Contains(string(raw), `"`+lit+`"`) {
				t.Errorf("%s no longer contains %q: remove the stale read-only entry", rel, lit)
			}
		}
	}
}

func TestSource_TheReadOnlyListIsExact(t *testing.T) {
	cloud := "processor/logshapeprocessor/cloudtrail.go"
	if h := deprecatedHits(cloud, `x := "rpc.service"`); len(h) != 0 {
		t.Errorf("allowed pair is flagged: %v", h)
	}
	if h := deprecatedHits("processor/logshapeprocessor/other.go", `x := "rpc.service"`); len(h) != 1 {
		t.Errorf("same literal in another file must fail, got %v", h)
	}
	if h := deprecatedHits(cloud, `x := "rpc.service"; y := "rpc.system"`); len(h) != 1 {
		t.Errorf("another literal in the same file must fail, got %v", h)
	}
}

func TestSource_TheCheckFindsWhatItNames(t *testing.T) {
	for _, bad := range []string{`attrs.PutStr("code.filepath", f)`, `m["deployment.environment"] = "lab"`, `"rpc.service"`, `"event.name"`} {
		if !deprecatedName.MatchString(bad) {
			t.Errorf("the check misses %q", bad)
		}
	}
	for _, ok := range []string{`"deployment.environment.name"`, `"rpc.system.name"`, `"code.function.name"`, `"code.file.path"`} {
		if deprecatedName.MatchString(ok) {
			t.Errorf("the check flags %q", ok)
		}
	}
}
