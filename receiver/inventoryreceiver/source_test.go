// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package inventoryreceiver

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func sourceFiles(t *testing.T) map[string]string {
	t.Helper()
	entries, err := os.ReadDir(".")
	require.NoError(t, err)
	out := map[string]string{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		b, err := os.ReadFile(filepath.Clean(e.Name()))
		require.NoError(t, err)
		out[e.Name()] = string(b)
	}
	return out
}

func importsOf(t *testing.T, name, src string) []string {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), name, src, parser.ImportsOnly)
	require.NoError(t, err, name)
	var out []string
	for _, imp := range f.Imports {
		p, err := strconv.Unquote(imp.Path.Value)
		require.NoError(t, err)
		out = append(out, p)
	}
	return out
}

func TestSourceRunsNoExternalProgramAndUsesNoCgo(t *testing.T) {
	files := sourceFiles(t)
	require.NotEmpty(t, files)

	for name, src := range files {
		for _, p := range importsOf(t, name, src) {
			assert.NotEqual(t, "os/exec", p, name)
			assert.NotEqual(t, "C", p, name)
			if p == "unsafe" {
				assert.Equal(t, "winapi_windows.go", name, "only the Windows adapter may use unsafe")
			}
		}
	}
}

func TestSourceImportsNeitherTheSmartNorTheIPMIReceiver(t *testing.T) {
	for name, src := range sourceFiles(t) {
		for _, p := range importsOf(t, name, src) {
			assert.NotContains(t, p, "smartreceiver", name)
			assert.NotContains(t, p, "smartdecode", name)
			assert.NotContains(t, p, "ipmireceiver", name)
		}
	}
}

func TestOnlyTheWindowsAdapterImportsTheWindowsPackages(t *testing.T) {
	for name, src := range sourceFiles(t) {
		for _, p := range importsOf(t, name, src) {
			if strings.HasPrefix(p, "golang.org/x/sys/windows") || p == "syscall" {
				assert.Equal(t, "winapi_windows.go", name)
			}
		}
	}
}

func TestSourceHasNoUptimeOrBootSeries(t *testing.T) {
	for name, src := range sourceFiles(t) {
		lower := strings.ToLower(src)
		assert.NotContains(t, lower, `"inventory.uptime`, name)
		assert.NotContains(t, lower, `"inventory.boot`, name)
		assert.NotContains(t, lower, "uptime", name)
		assert.NotContains(t, lower, "proc/uptime", name)
	}
}

// The collector may read its own environment (proc/self/environ) to find a container runtime; no other
// process environment is read.
func TestSourceNeverOpensTheHostUUIDOrAnotherProcessEnvironment(t *testing.T) {
	for name, src := range sourceFiles(t) {
		assert.NotContains(t, src, "product_uuid", name)
		own := strings.ReplaceAll(src, "proc/self/environ", "")
		assert.NotContains(t, own, "/environ", name)
		assert.NotContains(t, own, "environ\"", name)
	}
}
