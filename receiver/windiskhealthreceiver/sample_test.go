// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package windiskhealthreceiver

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/confmap"
	"go.yaml.in/yaml/v3"
)

func readRepoFile(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile("../../" + rel)
	require.NoError(t, err)
	return string(b)
}

// section returns the text from the first start marker to the next second-level heading.
func section(t *testing.T, doc, start, next string) string {
	t.Helper()
	i := strings.Index(doc, start)
	require.GreaterOrEqual(t, i, 0, "%q is missing", start)
	rest := doc[i:]
	if j := strings.Index(rest[3:], next); j >= 0 {
		rest = rest[:j+3]
	}
	return rest
}

func TestSample_TheDiskHealthConfigLoadsAndHoldsTheInterimPath(t *testing.T) {
	text := readRepoFile(t, "test/configs/windows-disk-health.yaml")
	var doc struct {
		Receivers map[string]map[string]any `yaml:"receivers"`
	}
	require.NoError(t, yaml.Unmarshal([]byte(text), &doc))

	cfg := createDefaultConfig().(*Config)
	require.NoError(t, confmap.NewFromStringMap(doc.Receivers["windiskhealth"]).Unmarshal(cfg))
	assert.NoError(t, cfg.Validate())
	assert.Equal(t, 64, cfg.MaxDevices)

	assert.Contains(t, text, "object: PhysicalDisk")
	for _, source := range []string{"disk", "Ntfs", "stornvme", "Virtual Disk Service"} {
		assert.Contains(t, text, "@Name='"+source+"'", source)
	}
	assert.Contains(t, text, "channel: System")
	assert.NotContains(t, text, "otel-main.example.invalid:4318/", "the gateway stays a placeholder")
}

func TestDocs_TheWindowsGuideStatesTheReceiversRules(t *testing.T) {
	doc := readRepoFile(t, "docs/windows-edge.md")
	guide := section(t, doc, "## Disk health", "\n## ")
	for _, want := range []string{
		"needs LocalSystem",
		"The Windows build of 0.3.16 and earlier has no disk SMART receiver",
		"interim path",
		"windows.physicaldisk.*",
		"emits no I/O rate or queue series",
		"same names, units and attributes as the Linux `smart` receiver",
		"Intel RST (VMD)",
		"skipped and counted",
		"`Virtual Disk Service`",
		"No `disk` or `stornvme` event has been seen yet",
	} {
		assert.Contains(t, guide, want)
	}
}

func TestChangelog_RecordsTheReceiverAndItsGrowth(t *testing.T) {
	// The entry stays in the release section it shipped in, so the whole file is read.
	log := readRepoFile(t, "CHANGELOG.md")
	assert.Contains(t, log, "`windiskhealth`")
	assert.Regexp(t, `otelcol-edge-\d+\.\d+\.\d+-windows-amd64\.exe`, log)
}
