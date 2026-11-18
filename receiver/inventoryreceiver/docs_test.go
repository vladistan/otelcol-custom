// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package inventoryreceiver

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func readDoc(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(rel)
	require.NoError(t, err)
	return string(b)
}

func TestMetricsDocStatesTheKeysMetricsAndWarnings(t *testing.T) {
	doc := readDoc(t, "../../docs/otel-metrics.md")

	for _, want := range []string{
		metricCPU, metricMemory, metricBoard, metricBIOS, metricSystem, metricDrive, metricGPU, metricStatus,
		"collection_interval", "max_drives",
		"Serial numbers are sensitive", "at least 25 h", "no uptime or boot time series",
		"otel_metrics_gauge", "retention", "Network cards", "Physical hosts only",
	} {
		assert.Contains(t, doc, want)
	}
}

// The BMC FRU series and fru_interval are defined by the ipmi module; the shared doc covers both
// receivers, so this package only checks that the doc keeps mentioning them.
func TestMetricsDocMentionsTheIPMIFRUSeries(t *testing.T) {
	doc := readDoc(t, "../../docs/otel-metrics.md")

	assert.Contains(t, doc, "inventory.bmc.fru.info")
	assert.Contains(t, doc, "fru_interval")
}

func TestReadmeAndChangelogMentionTheReceiver(t *testing.T) {
	assert.Contains(t, readDoc(t, "../../README.md"), "`inventory` receiver")
	changelog := readDoc(t, "../../CHANGELOG.md")
	assert.Contains(t, changelog, "`inventory` receiver")
	assert.Contains(t, changelog, "BMC FRU series")
	assert.Contains(t, changelog, "Binary size")
}

func TestSampleConfigNamesTheReceiverAndItsKeys(t *testing.T) {
	cfg := readDoc(t, "../../test/configs/edge-inventory.yaml")

	assert.Contains(t, cfg, "inventory:")
	assert.Contains(t, cfg, "collection_interval: 24h")
	assert.Contains(t, cfg, "receivers: [inventory]")
}
