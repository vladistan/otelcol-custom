// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package megaraidreceiver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/confmap"

	"github.com/vladistan/otelcol-custom/receiver/hwschema"
)

func readDoc(t *testing.T, rel string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "..", rel))
	require.NoError(t, err)
	return string(body)
}

// megaraidSection returns the MegaRAID part of docs/otel-metrics.md.
func megaraidSection(t *testing.T) string {
	doc := readDoc(t, "docs/otel-metrics.md")
	_, rest, ok := strings.Cut(doc, "### MegaRAID controllers")
	require.True(t, ok, "the MegaRAID section is missing")
	section, _, _ := strings.Cut(rest, "\n### ")
	return section
}

func TestDocsListEveryMetricTheReceiverSends(t *testing.T) {
	section := megaraidSection(t)
	for name := range specs {
		if strings.HasPrefix(name, "hw.physical_disk.") && name != hwschema.MetricPhysicalDiskSize {
			continue // SMART series are documented with the smart receiver in docs/smart.md
		}
		assert.Contains(t, section, "`"+name+"`", name)
	}
}

func TestDocsNameTheKeysTheCapabilityAndTheLimits(t *testing.T) {
	section := megaraidSection(t)

	for _, want := range []string{
		"collection_interval", "smart_interval", "create_node", "min_class", "max_per_scrape", "events.storage",
		"CAP_SYS_ADMIN", "/dev/megaraid_sas_ioctl_node", "2208", "3108", "Series per drive", "queues behind",
		"nothing on the controller is changed", "synthetic", "file_storage", "was available for a live run",
	} {
		assert.Contains(t, section, want)
	}
	assert.Contains(t, readDoc(t, "README.md"), "### MegaRAID controllers")
	assert.Contains(t, readDoc(t, "README.md"), "CAP_SYS_ADMIN")
}

func TestEveryConfigKeyOfTheDocsLoads(t *testing.T) {
	cfg := createDefaultConfig().(*Config)
	cm := confmap.NewFromStringMap(map[string]any{
		"collection_interval": "60s", "smart": false, "smart_interval": "15m", "create_node": false,
		"events": map[string]any{"enabled": true, "storage": "file_storage/megaraid", "min_class": "info", "max_per_scrape": 200},
	})

	require.NoError(t, cm.Unmarshal(cfg))
	require.NoError(t, cfg.Validate())
}

func TestTheSampleConfigKeysMatchTheReceiver(t *testing.T) {
	sample := readDoc(t, "test/configs/edge-megaraid.yaml")

	for _, key := range []string{"collection_interval", "smart:", "smart_interval", "create_node", "events:", "storage:", "min_class", "max_per_scrape"} {
		assert.Contains(t, sample, key)
	}
}
