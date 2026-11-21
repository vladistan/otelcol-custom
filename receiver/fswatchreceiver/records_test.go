// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

// cspell:ignore vstan

package fswatchreceiver

import (
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	excludedRecordsMetric = "otelcol_fswatch_records_excluded"
	scanRel               = `scans\2026-01\run-031.dat`
)

// isComplete reports whether a body is a write-complete body.
func isComplete(body string) bool { return strings.HasPrefix(body, completePrefix) }

// serviceNames returns the service.name resource attribute of every delivered batch.
func serviceNames(t *testing.T, r *rig) []string {
	t.Helper()
	var out []string
	for _, ld := range r.sink.AllLogs() {
		for i := 0; i < ld.ResourceLogs().Len(); i++ {
			v, ok := ld.ResourceLogs().At(i).Resource().Attributes().Get("service.name")
			if !ok {
				t.Fatalf("batch %d has no service.name", i)
			}
			out = append(out, v.Str())
		}
	}
	return out
}

func withRoot(root RootConfig) rigOption {
	return withConfig(func(c *Config) {
		c.Paths, c.Exclude = nil, nil
		root.Path = testRoot
		c.Roots = []RootConfig{root}
	})
}

func TestCreatedRecordHasPlainBodyAndSource(t *testing.T) {
	r := newRig(t)
	r.start()
	r.tree.put("run.dx", 5, ago(r.clock, time.Second))
	r.handle(r.added("run.dx"))
	got := r.records()
	if len(got) != 1 || got[0].body != "created" {
		t.Fatalf("want one record with body %q, got %+v", "created", got)
	}
	if got[0].attrs["log.source"] != "fswatch" {
		t.Errorf("log.source %v, want fswatch", got[0].attrs["log.source"])
	}
}

func TestWriteCompleteBodyHasRelativePathAndSize(t *testing.T) {
	r := newRig(t, withSettle(2*time.Second, time.Minute))
	r.start()
	rel := filepath.Join("scans", "2026-01", "run-031.dat")
	created := ago(r.clock, time.Second)
	r.tree.put(rel, 28467, created)
	r.handle(r.added(rel))
	r.clock.Advance(3 * time.Second)
	r.tick()
	got := r.records()
	if len(got) != 2 || got[0].body != "created" {
		t.Fatalf("want created then write complete, got %+v", got)
	}
	want := `write complete ` + scanRel + ` (27.8 KiB)`
	if got[1].body != want {
		t.Errorf("body %q, want %q", got[1].body, want)
	}
	if got[1].attrs["file.size"] != int64(28467) {
		t.Errorf("file.size %v, want 28467", got[1].attrs["file.size"])
	}
	if _, has := got[1].attrs["file.size_final"]; has {
		t.Errorf("file.size_final is renamed to file.size: %v", got[1].attrs)
	}
}

func TestWriteCompleteAtTimeoutKeepsLastSeenSize(t *testing.T) {
	r := newRig(t, withSettle(2*time.Second, 5*time.Second))
	r.start()
	created := ago(r.clock, time.Second)
	r.tree.put("busy.dx", 1, created)
	r.handle(r.added("busy.dx"))
	for i := int64(1); i <= 8; i++ {
		r.clock.Advance(oneSecond)
		r.tree.put("busy.dx", 1+i, created)
		r.tick()
	}
	got := r.records()
	if len(got) != 2 || !isComplete(got[1].body) || !strings.HasPrefix(got[1].body, "write complete busy.dx (") {
		t.Fatalf("want a write-complete record for busy.dx, got %+v", got)
	}
	size, ok := got[1].attrs["file.size"].(int64)
	if !ok || size <= 1 || size > 9 {
		t.Fatalf("file.size %v must be the last size seen, between 2 and 9", got[1].attrs["file.size"])
	}
	if want := completeBody("busy.dx", size); got[1].body != want {
		t.Errorf("body %q, want %q", got[1].body, want)
	}
}

func TestCompleteBodyOmitsUnknownSize(t *testing.T) {
	if got := completeBody(`a\b.dx`, -1); got != `write complete a\b.dx` {
		t.Errorf("got %q", got)
	}
}

func TestFormatSize(t *testing.T) {
	tests := map[int64]string{
		0: "0 B", 1: "1 B", 1023: "1023 B", 1024: "1.0 KiB", 28467: "27.8 KiB",
		1<<20 - 1: "1024.0 KiB", 1 << 20: "1.0 MiB", 5 << 30: "5.0 GiB", 3 << 40: "3.0 TiB",
	}
	for in, want := range tests {
		if got := formatSize(in); got != want {
			t.Errorf("formatSize(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestRecordAttributesUseTheNewNames(t *testing.T) {
	r := newRig(t)
	r.start()
	created := ago(r.clock, 3*time.Second)
	path := r.tree.put(filepath.Join("sub", "deep", "run.DX"), 7, created)
	r.handle(r.added(filepath.Join("sub", "deep", "run.DX")))
	want := map[string]any{
		"file.path": path, "file.name": "run.DX", "file.extension": "DX", "file.size": int64(7),
		"file.directory":             filepath.Dir(path),
		"vstan.fswatch.root":         testRoot,
		"vstan.fswatch.dir_relative": filepath.Join("sub", "deep"),
		"vstan.fswatch.catchup":      false,
		"log.source":                 "fswatch",
	}
	if got := r.records()[0].attrs; !reflect.DeepEqual(got, want) {
		t.Errorf("attrs\n got %v\nwant %v", got, want)
	}
}

func TestServiceNameIsTheRootName(t *testing.T) {
	r := newRig(t, withRoot(RootConfig{Name: "instrument-data", Exclude: []string{}}))
	r.start()
	r.tree.put("a.dx", 1, ago(r.clock, time.Second))
	r.handle(r.added("a.dx"))
	if got := serviceNames(t, r); !reflect.DeepEqual(got, []string{"instrument-data"}) {
		t.Errorf("service.name %v, want [instrument-data]", got)
	}
}

func TestServiceNameDefaultsToTheLowerCasedLastElement(t *testing.T) {
	r := newRig(t, withRoot(RootConfig{Exclude: []string{}}))
	r.start()
	r.tree.put("a.dx", 1, ago(r.clock, time.Second))
	r.handle(r.added("a.dx"))
	if got := serviceNames(t, r); !reflect.DeepEqual(got, []string{"cds"}) {
		t.Errorf("service.name %v, want [cds]", got)
	}
}

func TestDefaultRootName(t *testing.T) {
	tests := map[string]string{
		`D:\Projects`:          "projects",
		`D:\Data\Instrument\`:  "instrument",
		`D:/Data/Spectra`:      "spectra",
		`\\lab\share\Runs`:     "runs",
		`\\lab\share`:          "share",
		`D:\`:                  "d",
		`\\?\UNC\lab\share\X1`: "x1",
	}
	for in, want := range tests {
		if got := defaultRootName(in); got != want {
			t.Errorf("defaultRootName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDefaultExclusionsApplyWithoutAnExcludeList(t *testing.T) {
	r := newRig(t, withConfig(func(c *Config) { c.Exclude = nil }))
	r.start()
	skipped := []string{
		filepath.Join(".tmp.driveupload", "123"),
		filepath.Join("sub", ".tmp.drivedownload", "456"),
		"desktop.ini",
		filepath.Join("a", "b", "Desktop.INI"),
		filepath.Join("scans", "index.db-journal"),
	}
	for _, rel := range skipped {
		r.tree.put(rel, 1, ago(r.clock, time.Second))
		r.handle(r.added(rel))
	}
	r.tree.put(filepath.Join("scans", "good.0"), 1, ago(r.clock, time.Second))
	r.handle(r.added(filepath.Join("scans", "good.0")))
	got := r.records()
	if len(got) != 1 || got[0].attrs["file.name"] != "good.0" {
		t.Fatalf("want only good.0, got %+v", got)
	}
	if n := r.counter(excludedRecordsMetric); n != int64(len(skipped)) {
		t.Errorf("excluded counter %d, want %d", n, len(skipped))
	}
}

func TestExplicitExcludeReplacesTheDefaults(t *testing.T) {
	r := newRig(t, withRoot(RootConfig{Name: "n", Exclude: []string{`**/*.bak`}}))
	r.start()
	for _, rel := range []string{"desktop.ini", "x.bak"} {
		r.tree.put(rel, 1, ago(r.clock, time.Second))
		r.handle(r.added(rel))
	}
	got := r.records()
	if len(got) != 1 || got[0].attrs["file.name"] != "desktop.ini" {
		t.Fatalf("want only desktop.ini, got %+v", got)
	}
}

func TestEmptyExcludeListTurnsTheDefaultsOff(t *testing.T) {
	r := newRig(t, withRoot(RootConfig{Name: "n", Exclude: []string{}}))
	r.start()
	r.tree.put("desktop.ini", 1, ago(r.clock, time.Second))
	r.handle(r.added("desktop.ini"))
	if n := len(r.records()); n != 1 {
		t.Fatalf("want 1 record, got %d", n)
	}
}

func TestExcludedCounterCarriesTheRootName(t *testing.T) {
	r := newRig(t, withRoot(RootConfig{Name: "instrument-data"}))
	r.start()
	r.tree.put("desktop.ini", 1, ago(r.clock, time.Second))
	r.handle(r.added("desktop.ini"))
	if n := r.counter(excludedRecordsMetric, "root", "instrument-data"); n != 1 {
		t.Errorf("excluded counter for the root %d, want 1", n)
	}
}

func TestRootsAndLegacyPathsBuildOneSpecEach(t *testing.T) {
	cfg := validConfig()
	cfg.Exclude = []string{`**/*.x`}
	cfg.Roots = []RootConfig{{Path: `E:\Opus`, Name: "opus"}}
	specs := cfg.rootSpecs()
	if len(specs) != 2 || specs[0].path != `D:\Projects` || specs[0].name != "projects" ||
		specs[1].path != `E:\Opus` || specs[1].name != "opus" {
		t.Fatalf("specs %+v", specs)
	}
	if !reflect.DeepEqual(specs[0].patterns(), []string{`**/*.x`}) || !reflect.DeepEqual(specs[1].patterns(), defaultExclude) {
		t.Errorf("exclude lists %v and %v", specs[0].patterns(), specs[1].patterns())
	}
}

func TestValidateRoots(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{"roots alone are valid", func(c *Config) { c.Paths = nil; c.Roots = []RootConfig{{Path: `D:\a`, Name: "a"}} }, ""},
		{"roots and paths together", func(c *Config) { c.Roots = []RootConfig{{Path: `E:\b`}} }, ""},
		{"neither", func(c *Config) { c.Paths = nil }, "paths must hold at least one root"},
		{"relative root path", func(c *Config) { c.Roots = []RootConfig{{Path: `rel`}} }, "must be absolute"},
		{"root nested in a path", func(c *Config) { c.Roots = []RootConfig{{Path: `D:\Projects\sub`}} }, "nested"},
		{"duplicate across lists", func(c *Config) { c.Roots = []RootConfig{{Path: `d:\projects`}} }, "duplicate"},
		{"bad root exclude glob", func(c *Config) { c.Roots = []RootConfig{{Path: `E:\b`, Exclude: []string{`**/[`}}} }, "exclude"},
		{"empty root path", func(c *Config) { c.Roots = []RootConfig{{Name: "x"}} }, "must be absolute"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig()
			tt.mutate(cfg)
			err := cfg.Validate()
			switch {
			case tt.want == "" && err != nil:
				t.Fatalf("want valid, got %v", err)
			case tt.want != "" && err == nil:
				t.Fatalf("want error containing %q, got nil", tt.want)
			case tt.want != "" && !strings.Contains(err.Error(), tt.want):
				t.Fatalf("want error containing %q, got %v", tt.want, err)
			}
		})
	}
}

func defaultExcludeRig(t *testing.T) *rig {
	t.Helper()
	return newRig(t, withConfig(func(c *Config) { c.Exclude = nil }))
}

func TestCatchUpCountsEveryExcludedFile(t *testing.T) {
	r := defaultExcludeRig(t)
	old := ago(r.clock, time.Hour)
	for _, rel := range []string{"desktop.ini", filepath.Join("a", "desktop.ini"), filepath.Join("a", "b", "Desktop.INI")} {
		r.tree.put(rel, 1, old)
	}
	r.rw.cfg.StartAt = startAtBeginning
	r.start()
	if n := len(r.records()); n != 0 {
		t.Fatalf("excluded files must emit no record, got %d", n)
	}
	if n := r.counter(excludedRecordsMetric, "root", r.rw.name); n != 3 {
		t.Errorf("excluded counter %d after the catch-up scan, want 3", n)
	}
}

func TestCatchUpCountsAPrunedDirectoryOnce(t *testing.T) {
	r := defaultExcludeRig(t)
	old := ago(r.clock, time.Hour)
	for _, rel := range []string{"1", "2", "3"} {
		r.tree.put(filepath.Join(".tmp.driveupload", rel), 1, old)
	}
	r.tree.put(filepath.Join("sub", ".tmp.drivedownload", "x"), 1, old)
	r.rw.cfg.StartAt = startAtBeginning
	r.start()
	if n := len(r.records()); n != 0 {
		t.Fatalf("pruned directories must emit no record, got %d", n)
	}
	if n := r.counter(excludedRecordsMetric, "root", r.rw.name); n != 2 {
		t.Errorf("excluded counter %d, want 2 (one for each pruned directory)", n)
	}
}

func TestNewDirectoryCountsItsExcludedEntries(t *testing.T) {
	r := defaultExcludeRig(t)
	r.start()
	created := ago(r.clock, time.Second)
	r.tree.putDir("batch")
	r.tree.put(filepath.Join("batch", "ok.dx"), 1, created)
	r.tree.put(filepath.Join("batch", ".tmp.driveupload", "1"), 1, created)
	r.tree.put(filepath.Join("batch", ".tmp.driveupload", "2"), 1, created)
	r.tree.put(filepath.Join("batch", "desktop.ini"), 1, created)
	r.handle(r.added("batch"))
	got := r.records()
	if len(got) != 1 || got[0].attrs["file.name"] != "ok.dx" {
		t.Fatalf("want only ok.dx, got %+v", got)
	}
	if n := r.counter(excludedRecordsMetric, "root", r.rw.name); n != 2 {
		t.Errorf("excluded counter %d, want 2 (the pruned directory and desktop.ini)", n)
	}
}

func TestValidateExcludeAndNames(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{"exclude with roots only", func(c *Config) {
			c.Paths = nil
			c.Exclude = []string{`**/*.x`}
			c.Roots = []RootConfig{{Path: `D:\a`, Name: "a"}}
		}, "exclude applies to paths only"},
		{"exclude with paths and roots is fine", func(c *Config) {
			c.Exclude = []string{`**/*.x`}
			c.Roots = []RootConfig{{Path: `E:\b`}}
		}, ""},
		{"same explicit name", func(c *Config) {
			c.Paths = nil
			c.Roots = []RootConfig{{Path: `D:\a`, Name: "x"}, {Path: `E:\b`, Name: "x"}}
		}, "duplicate root name"},
		{"names differ in case", func(c *Config) {
			c.Paths = nil
			c.Roots = []RootConfig{{Path: `D:\a`, Name: "Data"}, {Path: `E:\b`, Name: "data"}}
		}, "duplicate root name"},
		{"defaulted names collide", func(c *Config) { c.Paths = []string{`D:\a`, `E:\a`} }, "duplicate root name"},
		{"explicit name resolves the collision", func(c *Config) {
			c.Paths = nil
			c.Roots = []RootConfig{{Path: `D:\a`}, {Path: `E:\a`, Name: "other"}}
		}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig()
			tt.mutate(cfg)
			err := cfg.Validate()
			switch {
			case tt.want == "" && err != nil:
				t.Fatalf("want valid, got %v", err)
			case tt.want != "" && err == nil:
				t.Fatalf("want error containing %q, got nil", tt.want)
			case tt.want != "" && !strings.Contains(err.Error(), tt.want):
				t.Fatalf("want error containing %q, got %v", tt.want, err)
			}
		})
	}
}

func TestSharedExcludeGlobErrorIsReportedOnceAndNamesTheKey(t *testing.T) {
	cfg := validConfig()
	cfg.Paths = []string{`D:\a`, `E:\b`, `F:\c`}
	cfg.Exclude = []string{`**/[`}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("want an error")
	}
	if n := strings.Count(err.Error(), "not a valid glob"); n != 1 {
		t.Errorf("the glob error appears %d times, want 1: %v", n, err)
	}
	if !strings.Contains(err.Error(), "exclude") {
		t.Errorf("the error must name the exclude key: %v", err)
	}
}

func TestRootExcludeGlobErrorNamesTheRoot(t *testing.T) {
	cfg := validConfig()
	cfg.Paths = nil
	cfg.Roots = []RootConfig{{Path: `E:\b`, Name: "b", Exclude: []string{`**/[`}}}
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "roots[0]") || !strings.Contains(err.Error(), strconv.Quote(`E:\b`)) {
		t.Fatalf("the error must name the roots key and the path, got %v", err)
	}
}
