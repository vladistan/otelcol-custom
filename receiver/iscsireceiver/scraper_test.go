// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package iscsireceiver

import (
	"errors"
	"sync/atomic"
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

type fakeSessions struct {
	list  []session
	err   error
	reads atomic.Int32
}

func (f *fakeSessions) sessions() ([]session, error) {
	f.reads.Add(1)
	return f.list, f.err
}

func twoSessions() []session {
	return []session{
		{target: "iqn.up", tpgt: "1", state: "LOGGED_IN", portal: "192.0.2.10:3260",
			connections: []connection{{portal: "192.0.2.10:3260", state: "LOGGED_IN"}}},
		{target: "iqn.down", tpgt: "2", state: "FAILED", portal: "192.0.2.11:3260",
			connections: []connection{{portal: "192.0.2.11:3260", state: "FAILED"}}},
	}
}

func metricNamed(t *testing.T, md pmetric.Metrics, name string) pmetric.Metric {
	t.Helper()
	require.Equal(t, 1, md.ResourceMetrics().Len())
	ms := md.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics()
	for i := 0; i < ms.Len(); i++ {
		if ms.At(i).Name() == name {
			return ms.At(i)
		}
	}
	t.Fatalf("metric %s not found", name)
	return pmetric.Metric{}
}

func attrs(dp pmetric.NumberDataPoint) map[string]string {
	m := map[string]string{}
	dp.Attributes().Range(func(k string, v pcommon.Value) bool {
		m[k] = v.Str()
		return true
	})
	return m
}

func TestScrapeSessionStateIsAnInfoGaugePerSession(t *testing.T) {
	s := newScraper(zap.NewNop(), &fakeSessions{list: twoSessions()})
	now := time.Unix(1700000000, 0)

	md := s.scrape(now)

	m := metricNamed(t, md, "iscsi.session.state")
	require.Equal(t, pmetric.MetricTypeGauge, m.Type())
	dps := m.Gauge().DataPoints()
	require.Equal(t, 2, dps.Len())
	assert.Equal(t, int64(1), dps.At(0).IntValue())
	assert.Equal(t, pcommon.NewTimestampFromTime(now), dps.At(0).Timestamp())
	assert.Equal(t, map[string]string{"state": "LOGGED_IN", "iscsi.target": "iqn.up", "iscsi.portal": "192.0.2.10:3260", "iscsi.tpgt": "1"}, attrs(dps.At(0)))
	assert.Equal(t, map[string]string{"state": "FAILED", "iscsi.target": "iqn.down", "iscsi.portal": "192.0.2.11:3260", "iscsi.tpgt": "2"}, attrs(dps.At(1)))
}

func TestScrapeSessionCountIsTheNumberOfSessions(t *testing.T) {
	s := newScraper(zap.NewNop(), &fakeSessions{list: twoSessions()})

	md := s.scrape(time.Now())

	m := metricNamed(t, md, "iscsi.session.count")
	require.Equal(t, 1, m.Gauge().DataPoints().Len())
	assert.Equal(t, int64(2), m.Gauge().DataPoints().At(0).IntValue())
	assert.Equal(t, "{session}", m.Unit())
}

func TestScrapeConnectionStateIsOnePerConnection(t *testing.T) {
	list := twoSessions()
	list[0].connections = append(list[0].connections, connection{portal: "192.0.2.20:3260", state: "LOGGED_IN"})
	s := newScraper(zap.NewNop(), &fakeSessions{list: list})

	md := s.scrape(time.Now())

	m := metricNamed(t, md, "iscsi.connection.state")
	dps := m.Gauge().DataPoints()
	require.Equal(t, 3, dps.Len())
	assert.Equal(t, int64(1), dps.At(0).IntValue())
	assert.Equal(t, map[string]string{"state": "LOGGED_IN", "iscsi.target": "iqn.up", "iscsi.portal": "192.0.2.10:3260"}, attrs(dps.At(0)))
	assert.Equal(t, "192.0.2.20:3260", attrs(dps.At(1))["iscsi.portal"])
}

func TestScrapeWithNoSessionsLogsOneInfoLineAndSendsNothing(t *testing.T) {
	core, logs := observer.New(zapcore.InfoLevel)
	s := newScraper(zap.New(core), &fakeSessions{err: errNoSessions})

	for range 3 {
		assert.Zero(t, s.scrape(time.Now()).MetricCount())
	}

	assert.Equal(t, 1, logs.FilterLevelExact(zapcore.InfoLevel).Len())
}

func TestScrapeLogsAgainAfterSessionsComeAndGo(t *testing.T) {
	core, logs := observer.New(zapcore.InfoLevel)
	src := &fakeSessions{err: errNoSessions}
	s := newScraper(zap.New(core), src)

	s.scrape(time.Now())
	src.err, src.list = nil, twoSessions()
	assert.NotZero(t, s.scrape(time.Now()).MetricCount())
	src.err, src.list = errNoSessions, nil
	s.scrape(time.Now())

	assert.Equal(t, 2, logs.FilterLevelExact(zapcore.InfoLevel).Len())
}

func TestScrapeOtherErrorWarnsOncePerEpisode(t *testing.T) {
	core, logs := observer.New(zapcore.InfoLevel)
	src := &fakeSessions{err: errors.New("boom")}
	s := newScraper(zap.New(core), src)

	for range 3 {
		assert.Zero(t, s.scrape(time.Now()).MetricCount())
	}

	assert.Equal(t, 1, logs.FilterLevelExact(zapcore.WarnLevel).Len())
}

func TestScrapeSessionWithoutAConnectionHasNoConnectionMetric(t *testing.T) {
	s := newScraper(zap.NewNop(), &fakeSessions{list: []session{{target: "iqn.free", tpgt: "1", state: "FREE"}}})

	md := s.scrape(time.Now())

	require.Equal(t, 1, md.ResourceMetrics().Len())
	ms := md.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics()
	names := map[string]bool{}
	for i := 0; i < ms.Len(); i++ {
		names[ms.At(i).Name()] = true
	}
	assert.True(t, names["iscsi.session.state"])
	assert.True(t, names["iscsi.session.count"])
	assert.False(t, names["iscsi.connection.state"])
	state := metricNamed(t, md, "iscsi.session.state").Gauge().DataPoints().At(0)
	assert.Equal(t, map[string]string{"state": "FREE", "iscsi.target": "iqn.free", "iscsi.portal": "", "iscsi.tpgt": "1"}, attrs(state))
}
