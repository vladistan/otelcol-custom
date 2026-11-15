// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package ethtoolreceiver

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The receiver reads statistics in the collector process: no external command, no cgo.
func TestSourceImportsNoExecAndNoCgo(t *testing.T) {
	files, err := filepath.Glob("*.go")
	require.NoError(t, err)
	require.NotEmpty(t, files)

	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), name, nil, parser.ImportsOnly)
		require.NoError(t, err, name)
		for _, imp := range f.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			assert.NotEqual(t, "os/exec", path, name)
			assert.NotEqual(t, "C", path, name)
			assert.NotContains(t, path, "safchain/ethtool", name)
		}
	}
}

func TestSourceHasNoStatsRegexOption(t *testing.T) {
	src, err := os.ReadFile("config.go")
	require.NoError(t, err)

	assert.NotContains(t, string(src), "stats_regex")
}
