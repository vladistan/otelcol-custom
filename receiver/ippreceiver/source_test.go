// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package ippreceiver

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The receiver sends IPP over HTTP from the collector process: no external command.
func TestSourceImportsNoExec(t *testing.T) {
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
			assert.NotEqual(t, "os/exec", strings.Trim(imp.Path.Value, `"`), name)
		}
	}
}

// Get-Printer-Attributes is the only operation the receiver can send.
func TestSourceHasOneOperationCode(t *testing.T) {
	src, err := os.ReadFile("ipp.go")
	require.NoError(t, err)

	ops := regexp.MustCompile(`(?m)^\s*op\w+\s+uint16\s*=`).FindAll(src, -1)

	assert.Len(t, ops, 1)
	assert.Contains(t, string(src), "opGetPrinterAttributes")
}

func TestConfigHoldsNoCredential(t *testing.T) {
	src, err := os.ReadFile("config.go")
	require.NoError(t, err)

	for _, word := range []string{"password", "token", "secret", "username", "auth"} {
		assert.NotContains(t, strings.ToLower(string(src)), word)
	}
}

// The scrape collector docs state the series, the keys and the operation, and name no host or account.
func TestDocsListSeriesKeysAndOperation(t *testing.T) {
	docs, err := os.ReadFile(filepath.Join("..", "..", "docs", "scrape-collector.md"))
	require.NoError(t, err)
	text := string(docs)
	idx := strings.Index(text, "## IPP printer receiver")
	require.GreaterOrEqual(t, idx, 0, "docs/scrape-collector.md has no IPP printer receiver section")
	section := text[idx:]
	if next := regexp.MustCompile(`\n#{2,3} `).FindStringIndex(section[3:]); next != nil {
		section = section[:next[0]+3]
	}

	for _, want := range []string{
		"printer.ipp.state", "printer.ipp.state_reason", "printer.ipp.marker.level",
		"printer.ipp.reachable", "printer.ipp.accepting_jobs", "printer.ipp.queued_jobs",
		"collection_interval", "timeout", "printers", "name", "endpoint",
		"Get-Printer-Attributes", "hw.type",
	} {
		assert.Contains(t, section, want)
	}
	// Built from halves so this file holds no site name itself.
	for _, banned := range []string{"arse" + "nale", "porde" + "none"} {
		assert.NotContains(t, strings.ToLower(section), banned)
	}
	assert.NotRegexp(t, regexp.MustCompile(`\b\d{12}\b`), section, "no account id")
}
