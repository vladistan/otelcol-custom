// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package hwmonreceiver

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func hwTypesOf(t *testing.T, md pmetric.Metrics) map[string]map[string]bool {
	t.Helper()
	out := map[string]map[string]bool{}
	ms := md.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics()
	for i := 0; i < ms.Len(); i++ {
		m := ms.At(i)
		dps := m.Gauge().DataPoints()
		for j := 0; j < dps.Len(); j++ {
			v, ok := dps.At(j).Attributes().Get("hw.type")
			require.True(t, ok, "%s has a point with no hw.type", m.Name())
			if out[m.Name()] == nil {
				out[m.Name()] = map[string]bool{}
			}
			out[m.Name()][v.Str()] = true
		}
	}
	return out
}

func TestScrape_EveryHwMetricCarriesTheHwTypeOfItsSensorKind(t *testing.T) {
	s := newScraper(zap.NewNop(), testConfig(), &fakeSource{readings: sampleReadings()})

	got := hwTypesOf(t, s.scrape(time.Unix(1700000000, 0)))

	assert.Equal(t, map[string]map[string]bool{
		"hw.temperature":       {"temperature": true},
		"hw.temperature.limit": {"temperature": true},
		"hw.fan.speed":         {"fan": true},
		"hw.voltage":           {"voltage": true},
	}, got)
}

func TestScrape_TheHwTypeIsAnAttributeBesideTheIdentity(t *testing.T) {
	s := newScraper(zap.NewNop(), testConfig(), &fakeSource{readings: sampleReadings()})

	md := s.scrape(time.Unix(1700000000, 0))

	dp := dpByID(t, metricByName(t, md, "hw.fan.speed").Gauge().DataPoints(), "nct6775/fan1")
	attrs := dp.Attributes()
	for key, want := range map[string]string{"hw.id": "nct6775/fan1", "hw.name": "CPU fan", "hw.parent": "nct6775", "hw.type": "fan"} {
		v, ok := attrs.Get(key)
		require.True(t, ok, key)
		assert.Equal(t, want, v.Str(), key)
	}
	assert.Equal(t, 4, attrs.Len(), "no other attribute joined")
}

func TestHwType_ValuesFollowTheOpenTelemetryList(t *testing.T) {
	for k, want := range map[kind]string{
		kindTemperature: "temperature", kindFan: "fan", kindVoltage: "voltage", kindPowerSupply: "power_supply",
	} {
		got, ok := hwTypeOf(k)

		assert.True(t, ok)
		assert.Equal(t, want, got)
	}
}

func TestHwType_AKindWithNoValue_GivesNoTypeAndOneDebugLinePerKind(t *testing.T) {
	core, logs := observer.New(zapcore.DebugLevel)
	b := newBuilder(zap.New(core), pmetric.NewMetrics(), pcommon.NewTimestampFromTime(time.Unix(1, 0)))

	for range 3 {
		_, ok := b.hwType(kind(99))
		assert.False(t, ok)
	}

	require.Equal(t, 1, logs.Len())
	assert.Equal(t, zapcore.DebugLevel, logs.All()[0].Level)
	assert.Equal(t, int64(99), logs.All()[0].ContextMap()["kind"])
}
