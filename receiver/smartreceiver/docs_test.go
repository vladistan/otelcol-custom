// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package smartreceiver

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vladistan/otelcol-custom/receiver/hwschema"
)

func TestDocs_ListEveryMetricTheCapabilitiesTheUnitRulesTheBudgetAndTheRaidBoundary(t *testing.T) {
	raw, err := os.ReadFile("../../docs/smart.md")
	require.NoError(t, err)
	text := string(raw)

	for _, spec := range hwschema.PhysicalDiskMetrics {
		assert.Contains(t, text, "`"+spec.Name+"`", "metric table")
	}
	for _, want := range []string{
		"CAP_SYS_ADMIN", "CAP_SYS_RAWIO", "PrivateDevices", "DeviceAllow", "block-sd", "char-nvme",
		"## Series budget", "RAID controller", "otelcol_smart_devices_skipped", "smart_value_type",
	} {
		assert.Contains(t, text, want)
	}
	assert.NotContains(t, strings.ToLower(text), "smartctl_")
}

func TestSampleConfigLoadsIntoTheReceiverConfig(t *testing.T) {
	raw, err := os.ReadFile("../../test/configs/edge-smart.yaml")
	require.NoError(t, err)

	assert.Contains(t, string(raw), "smart:")
	assert.Contains(t, string(raw), "attributes: default")
}
