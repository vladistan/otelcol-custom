// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package smartreceiver

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/pmetric"

	"github.com/vladistan/otelcol-custom/receiver/smartdecode/smarttest"
)

const signatureGolden = "testdata/series-signature.golden"

// seriesSignature lists each series as name, unit, kind and attribute keys. The windiskhealth receiver
// reads the same file for the same fixtures, so the two receivers cannot drift apart.
func seriesSignature(md pmetric.Metrics) []string {
	seen := map[string]bool{}
	ms := md.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics()
	for i := 0; i < ms.Len(); i++ {
		m := ms.At(i)
		var dps pmetric.NumberDataPointSlice
		if m.Type() == pmetric.MetricTypeSum {
			dps = m.Sum().DataPoints()
		} else {
			dps = m.Gauge().DataPoints()
		}
		for j := 0; j < dps.Len(); j++ {
			var keys []string
			for k := range dps.At(j).Attributes().AsRaw() {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			seen[fmt.Sprintf("%s|%s|%s|%s", m.Name(), m.Unit(), m.Type().String(), strings.Join(keys, ","))] = true
		}
	}
	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func TestSeriesSignatureMatchesTheGoldenFile(t *testing.T) {
	bus := newBus()
	bus.addATA("sda", smarttest.HealthyATA("SN1"))
	bus.addNVMe("nvme0", smarttest.HealthyNVMe("NV1"))
	got := strings.Join(seriesSignature(newHarness(t, bus).scrape()), "\n") + "\n"

	if os.Getenv("UPDATE_GOLDEN") != "" {
		require.NoError(t, os.MkdirAll("testdata", 0o755))
		require.NoError(t, os.WriteFile(signatureGolden, []byte(got), 0o644))
	}
	want, err := os.ReadFile(signatureGolden)
	require.NoError(t, err)
	assert.Equal(t, string(want), got)
}
