// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package windiskhealthreceiver

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// productionFiles parses the non-test Go files of the package; comments are dropped, so only code is checked.
func productionFiles(t *testing.T) map[string]*ast.File {
	t.Helper()
	paths, err := filepath.Glob("*.go")
	require.NoError(t, err)
	out := map[string]*ast.File{}
	for _, p := range paths {
		if strings.HasSuffix(p, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), p, nil, 0)
		require.NoError(t, err, p)
		out[p] = f
	}
	require.NotEmpty(t, out)
	return out
}

func importPaths(t *testing.T, f *ast.File) []string {
	t.Helper()
	var out []string
	for _, imp := range f.Imports {
		p, err := strconv.Unquote(imp.Path.Value)
		require.NoError(t, err)
		out = append(out, p)
	}
	return out
}

// stringLiterals returns the unquoted string literals of a file.
func stringLiterals(t *testing.T, f *ast.File) []string {
	t.Helper()
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
			s, err := strconv.Unquote(lit.Value)
			require.NoError(t, err)
			out = append(out, s)
		}
		return true
	})
	return out
}

// selectors returns every pkg.Name selector of a file as "pkg.Name".
func selectors(f *ast.File) []string {
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		if sel, ok := n.(*ast.SelectorExpr); ok {
			if x, ok := sel.X.(*ast.Ident); ok {
				out = append(out, x.Name+"."+sel.Sel.Name)
			}
		}
		return true
	})
	return out
}

func TestSource_NoExecAndNoCgo(t *testing.T) {
	banned := []string{"os.StartProcess", "syscall.Exec", "syscall.ForkExec"}
	for file, f := range productionFiles(t) {
		assert.NotContains(t, importPaths(t, f), "os/exec", file)
		assert.NotContains(t, importPaths(t, f), "C", file)
		for _, sel := range selectors(f) {
			assert.NotContains(t, banned, sel, file)
		}
		for _, s := range stringLiterals(t, f) {
			lower := strings.ToLower(s)
			for _, program := range []string{"wmic", "powershell", "smartctl", "get-physicaldisk"} {
				assert.NotContains(t, lower, program, file)
			}
		}
	}
}

func TestSource_OnlyTheWindowsFileCallsDeviceIoControl(t *testing.T) {
	for file, f := range productionFiles(t) {
		if file == "ioctl_windows.go" {
			assert.Contains(t, selectors(f), "windows.DeviceIoControl")
			continue
		}
		assert.NotContains(t, selectors(f), "windows.DeviceIoControl", file, "the control calls go through the controller interface")
		assert.NotContains(t, importPaths(t, f), "golang.org/x/sys/windows", file)
	}
}

func TestSource_TheDecodersAreSmartdecodes(t *testing.T) {
	own := []string{"ParseIdentify", "ParseSmartData", "ParseNVMeSmartLog", "ParseReturnStatus", "parseSmart", "decodeSmart"}
	var imported bool
	for file, f := range productionFiles(t) {
		imported = imported || slices.Contains(importPaths(t, f), "github.com/vladistan/otelcol-custom/receiver/smartdecode")
		for _, d := range f.Decls {
			if fn, ok := d.(*ast.FuncDecl); ok {
				assert.NotContains(t, own, fn.Name.Name, file)
			}
		}
	}
	assert.True(t, imported, "the package imports smartdecode")
}

func TestSource_NoIORateOrQueueSeries(t *testing.T) {
	for file, f := range productionFiles(t) {
		for _, s := range stringLiterals(t, f) {
			lower := strings.ToLower(s)
			for _, word := range []string{"queue", "iops", "perfcounter", "disk.io.rate"} {
				assert.NotContains(t, lower, word, file)
			}
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if id, ok := n.(*ast.Ident); ok {
				lower := strings.ToLower(id.Name)
				assert.NotContains(t, lower, "queue", file)
				assert.NotContains(t, lower, "iops", file)
				assert.NotContains(t, lower, "perfcounter", file)
			}
			return true
		})
		for _, sel := range selectors(f) {
			assert.False(t, strings.HasPrefix(sel, "windows.PhysicalDisk"), "%s: %s", file, sel)
		}
	}
}

func TestSource_MetricNamesLiveInHwschemaOnly(t *testing.T) {
	for file, f := range productionFiles(t) {
		if file == "telemetry.go" {
			continue
		}
		for _, s := range stringLiterals(t, f) {
			assert.False(t, strings.HasPrefix(s, "hw."), "%s: %q", file, s)
		}
	}
}
