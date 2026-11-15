// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package tightvncprocessor

import (
	"go/parser"
	"go/token"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// The processor only reshapes records that filelog already read, so a file-reading import
// means it has started to tail files itself.
func TestPackage_ImportsNoFileReadingPackage(t *testing.T) {
	banned := []string{"os", "io", "io/fs", "io/ioutil", "bufio", "path/filepath", "syscall", "golang.org/x/sys/windows"}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package directory: %v", err)
	}
	checked := 0
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		checked++
		file, err := parser.ParseFile(token.NewFileSet(), name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, spec := range file.Imports {
			path, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				t.Fatalf("unquote import in %s: %v", name, err)
			}
			if slices.Contains(banned, path) {
				t.Errorf("%s imports %q", name, path)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no non-test source files found to check")
	}
}
