// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package iscsireceiver

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The receiver reads sysfs in the collector process: no command, no cgo, no raw syscall, no
// device node, and nothing written.
func TestSourceReadsSysfsOnly(t *testing.T) {
	forbiddenImports := map[string]bool{"os/exec": true, "C": true, "unsafe": true, "net/http": true}
	forbiddenOS := map[string]bool{
		"OpenFile": true, "Create": true, "WriteFile": true, "Remove": true, "RemoveAll": true, "Rename": true,
		"Mkdir": true, "MkdirAll": true, "Chmod": true, "Chown": true, "Symlink": true, "Truncate": true,
	}
	files, err := filepath.Glob("*.go")
	require.NoError(t, err)
	checked := 0
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		checked++
		f, err := parser.ParseFile(token.NewFileSet(), name, nil, 0)
		require.NoError(t, err, name)
		for _, imp := range f.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			assert.False(t, forbiddenImports[path], "%s imports %s", name, path)
			if path == "syscall" {
				continue // only the error numbers, checked below
			}
		}
		ast.Inspect(f, func(n ast.Node) bool {
			switch v := n.(type) {
			case *ast.BasicLit:
				if v.Kind == token.STRING {
					s, _ := strconv.Unquote(v.Value)
					assert.False(t, strings.HasPrefix(s, "/dev"), "%s names a device node: %s", name, s)
				}
			case *ast.SelectorExpr:
				if id, ok := v.X.(*ast.Ident); ok {
					assert.False(t, id.Name == "os" && forbiddenOS[v.Sel.Name], "%s calls os.%s", name, v.Sel.Name)
					if id.Name == "syscall" {
						assert.True(t, strings.HasPrefix(v.Sel.Name, "E"), "%s uses syscall.%s, only error numbers are allowed", name, v.Sel.Name)
					}
				}
			}
			return true
		})
	}
	assert.GreaterOrEqual(t, checked, 7, "the sources were found")
}
