// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package megaraidreceiver

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
)

func TestBatteryChargeStaysBetweenZeroAndOne(t *testing.T) {
	for name, tt := range map[string]struct {
		pct  int
		want float64
	}{"over": {250, 1}, "under": {-5, 0}, "half": {50, 0.5}} {
		t.Run(name, func(t *testing.T) {
			md := pmetric.NewMetrics()
			b := newBuilder(md, pcommon.Timestamp(1), pcommon.Timestamp(2))
			(&snapshot{}).emitBBU(b, "0000:03:00.0", bbuInfo{ChargePct: tt.pct, hasCapacity: true})

			p, ok := find(md, metricBatteryCharge, "hw.id", "0000:03:00.0/bbu")

			assert.True(t, ok)
			assert.Equal(t, tt.want, p.val)
		})
	}
}
