// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package ethtoolreceiver

import (
	"fmt"
	"math"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.uber.org/zap/zapcore"
)

// seriesOf lists every metric of this name; the receiver emits a sum and a gauge under one statistic name.
func seriesOf(md pmetric.Metrics, name string) []pmetric.Metric {
	var out []pmetric.Metric
	for i := 0; i < md.ResourceMetrics().Len(); i++ {
		sms := md.ResourceMetrics().At(i).ScopeMetrics()
		for j := 0; j < sms.Len(); j++ {
			for k := 0; k < sms.At(j).Metrics().Len(); k++ {
				if m := sms.At(j).Metrics().At(k); m.Name() == name {
					out = append(out, m)
				}
			}
		}
	}
	return out
}

// statisticPoints maps "interface/statistic" to its value for the metric of the given type.
func statisticPoints(t *testing.T, md pmetric.Metrics, typ pmetric.MetricType) map[string]int64 {
	t.Helper()
	out := map[string]int64{}
	for _, m := range seriesOf(md, "network.interface.statistic") {
		if m.Type() != typ {
			continue
		}
		var dps pmetric.NumberDataPointSlice
		if typ == pmetric.MetricTypeSum {
			dps = m.Sum().DataPoints()
		} else {
			dps = m.Gauge().DataPoints()
		}
		for i := 0; i < dps.Len(); i++ {
			iface, _ := dps.At(i).Attributes().Get("network.interface.name")
			stat, _ := dps.At(i).Attributes().Get("statistic")
			out[iface.Str()+"/"+stat.Str()] = dps.At(i).IntValue()
		}
	}
	return out
}

func statisticsConfig(entries ...Statistic) *Config {
	cfg := testConfig()
	cfg.Statistics = entries
	cfg.MaxStatistics = defaultMaxStatistics
	return cfg
}

func queueNIC(queues int) *fakeNIC {
	nic := enaNIC()
	for q := 0; q < queues; q++ {
		nic.stats["eth0"] = append(nic.stats["eth0"], stat{fmt.Sprintf("queue_%03d_tx_cnt", q), uint64(q)})
	}
	return nic
}

func TestScrapeConntrackAvailableIsAGaugeWithTheTwoAttributes(t *testing.T) {
	nic := enaNIC()
	nic.stats["eth0"] = append(nic.stats["eth0"], stat{"conntrack_allowance_available", 136812})
	s, _ := newTestScraper(t, testConfig(), nic)

	md := s.scrape(time.Unix(2000, 0))

	ms := seriesOf(md, "network.allowance.available")
	require.Len(t, ms, 1)
	assert.Equal(t, pmetric.MetricTypeGauge, ms[0].Type())
	assert.Equal(t, "{connection}", ms[0].Unit())
	require.Equal(t, 1, ms[0].Gauge().DataPoints().Len())
	dp := ms[0].Gauge().DataPoints().At(0)
	assert.Equal(t, int64(136812), dp.IntValue())
	iface, _ := dp.Attributes().Get("network.interface.name")
	allow, _ := dp.Attributes().Get("allowance")
	assert.Equal(t, "eth0", iface.Str())
	assert.Equal(t, "conntrack", allow.Str())
	assert.Len(t, seriesOf(md, "network.allowance.exceeded"), 1, "the exceeded counters are unchanged")
}

func TestScrapeConntrackAvailableAbsentGivesNoPointAndOneInfoLinePerInterface(t *testing.T) {
	nic := enaNIC()
	nic.ifaces = []string{"eth0", "eth1"}
	nic.drivers["eth1"] = "ena"
	nic.stats["eth1"] = enaStats(20)
	s, logs := newTestScraper(t, testConfig(), nic)

	first := s.scrape(time.Unix(2000, 0))
	s.scrape(time.Unix(2060, 0))

	assert.Empty(t, seriesOf(first, "network.allowance.available"))
	assert.Equal(t, 2, logs.FilterMessageSnippet("conntrack_allowance_available").Len(), "one line per interface over two scrapes")
}

func TestScrapeConntrackAvailableInfoLineRepeatsAfterTheStatisticReturns(t *testing.T) {
	nic := enaNIC()
	s, logs := newTestScraper(t, testConfig(), nic)
	s.scrape(time.Unix(2000, 0))
	nic.stats["eth0"] = append(enaStats(10), stat{"conntrack_allowance_available", 5})
	s.scrape(time.Unix(2060, 0))
	nic.stats["eth0"] = enaStats(10)

	s.scrape(time.Unix(2120, 0))

	assert.Equal(t, 2, logs.FilterMessageSnippet("conntrack_allowance_available").Len())
}

func TestScrapeStatisticsEntriesGiveSumsAndGaugesAndAnOverlappedNameOnce(t *testing.T) {
	nic := enaNIC()
	nic.stats["eth0"] = append(nic.stats["eth0"],
		stat{"queue_0_tx_cnt", 7}, stat{"queue_1_tx_cnt", 8}, stat{"queue_0_rx_cnt", 9}, stat{"tx_timeout", 1})
	cfg := statisticsConfig(
		Statistic{Match: `^queue_[0-9]+_tx_cnt$`, Type: "sum"},
		Statistic{Match: `^queue_[0-9]+_`, Type: "gauge"},
	)
	s, _ := newTestScraper(t, cfg, nic)

	md := s.scrape(time.Unix(2000, 0))

	assert.Equal(t, map[string]int64{"eth0/queue_0_tx_cnt": 7, "eth0/queue_1_tx_cnt": 8},
		statisticPoints(t, md, pmetric.MetricTypeSum))
	assert.Equal(t, map[string]int64{"eth0/queue_0_rx_cnt": 9},
		statisticPoints(t, md, pmetric.MetricTypeGauge), "a name two entries match is under the first only")
	for _, m := range seriesOf(md, "network.interface.statistic") {
		if m.Type() == pmetric.MetricTypeSum {
			assert.True(t, m.Sum().IsMonotonic())
			assert.Equal(t, pmetric.AggregationTemporalityCumulative, m.Sum().AggregationTemporality())
			assert.NotZero(t, m.Sum().DataPoints().At(0).StartTimestamp())
		}
	}
}

func TestScrapeWithoutAStatisticsListEmitsNoStatisticSeries(t *testing.T) {
	s, _ := newTestScraper(t, testConfig(), queueNIC(50))

	md := s.scrape(time.Unix(2000, 0))

	assert.Empty(t, seriesOf(md, "network.interface.statistic"))
}

func TestScrapeStatisticsAboveTheCapAreDroppedInSortedOrderWithOneWarning(t *testing.T) {
	cfg := statisticsConfig(Statistic{Match: `^queue_`, Type: "sum"})
	s, logs := newTestScraper(t, cfg, queueNIC(300))

	first := s.scrape(time.Unix(2000, 0))
	s.scrape(time.Unix(2060, 0))

	got := statisticPoints(t, first, pmetric.MetricTypeSum)
	require.Len(t, got, 200)
	names := make([]string, 0, len(got))
	for k := range got {
		names = append(names, k)
	}
	sort.Strings(names)
	assert.Equal(t, "eth0/queue_000_tx_cnt", names[0])
	assert.Equal(t, "eth0/queue_199_tx_cnt", names[199])
	assert.Equal(t, 1, countLevel(logs, zapcore.WarnLevel), "one warning for two scrapes")
}

func TestScrapeCapWarningReturnsAfterTheCountFallsBelowTheCap(t *testing.T) {
	cfg := statisticsConfig(Statistic{Match: `^queue_`, Type: "sum"})
	cfg.MaxStatistics = 5
	nic := queueNIC(10)
	s, logs := newTestScraper(t, cfg, nic)
	s.scrape(time.Unix(2000, 0))
	nic.stats["eth0"] = nic.stats["eth0"][:len(allowanceNames)+3]
	s.scrape(time.Unix(2060, 0))
	nic.stats["eth0"] = queueNIC(10).stats["eth0"]

	s.scrape(time.Unix(2120, 0))

	assert.Equal(t, 2, countLevel(logs, zapcore.WarnLevel))
}

func TestScrapeCapCountsAllInterfacesAndKeepsInterfaceOrder(t *testing.T) {
	nic := queueNIC(3)
	nic.ifaces = []string{"eth1", "eth0"}
	nic.drivers["eth1"] = "ena"
	nic.stats["eth1"] = queueNIC(3).stats["eth0"]
	cfg := statisticsConfig(Statistic{Match: `^queue_`, Type: "gauge"})
	cfg.MaxStatistics = 4
	s, _ := newTestScraper(t, cfg, nic)

	got := statisticPoints(t, s.scrape(time.Unix(2000, 0)), pmetric.MetricTypeGauge)

	assert.Equal(t, map[string]int64{
		"eth0/queue_000_tx_cnt": 0, "eth0/queue_001_tx_cnt": 1, "eth0/queue_002_tx_cnt": 2,
		"eth1/queue_000_tx_cnt": 0,
	}, got)
}

func TestScrapeEntryThatMatchesNothingLogsOneInfoLine(t *testing.T) {
	cfg := statisticsConfig(Statistic{Match: `^no_such_statistic$`, Type: "sum"})
	s, logs := newTestScraper(t, cfg, enaNIC())

	s.scrape(time.Unix(2000, 0))
	s.scrape(time.Unix(2060, 0))

	assert.Equal(t, 1, logs.FilterMessageSnippet("matches no statistic").Len())
}

func TestScrapeEntryInfoLineWaitsForAnInterfaceWithAMatchingDriver(t *testing.T) {
	cfg := statisticsConfig(Statistic{Match: `^queue_`, Type: "sum"})
	nic := enaNIC()
	nic.drivers["eth0"] = "virtio_net"
	s, logs := newTestScraper(t, cfg, nic)

	s.scrape(time.Unix(2000, 0))

	assert.Zero(t, logs.FilterMessageSnippet("matches no statistic").Len())
}

func TestScrapeEntryInfoLineReturnsAfterTheEntryMatchedOnce(t *testing.T) {
	cfg := statisticsConfig(Statistic{Match: `^queue_0_tx_cnt$`, Type: "sum"})
	nic := enaNIC()
	s, logs := newTestScraper(t, cfg, nic)
	s.scrape(time.Unix(2000, 0))
	nic.stats["eth0"] = append(enaStats(10), stat{"queue_0_tx_cnt", 1})
	s.scrape(time.Unix(2060, 0))
	nic.stats["eth0"] = enaStats(10)

	s.scrape(time.Unix(2120, 0))

	assert.Equal(t, 2, logs.FilterMessageSnippet("matches no statistic").Len())
}

func TestScrapeStatisticValueAboveInt64IsClamped(t *testing.T) {
	nic := enaNIC()
	nic.stats["eth0"] = append(nic.stats["eth0"], stat{"queue_0_tx_cnt", math.MaxUint64})
	s, _ := newTestScraper(t, statisticsConfig(Statistic{Match: `^queue_0`, Type: "sum"}), nic)

	got := statisticPoints(t, s.scrape(time.Unix(2000, 0)), pmetric.MetricTypeSum)

	assert.Equal(t, int64(math.MaxInt64), got["eth0/queue_0_tx_cnt"])
}

func TestScrapeSkipsAnEntryWhosePatternDoesNotCompile(t *testing.T) {
	nic := enaNIC()
	nic.stats["eth0"] = append(nic.stats["eth0"], stat{"queue_0_tx_cnt", 4})
	cfg := statisticsConfig(Statistic{Match: "(", Type: "sum"}, Statistic{Match: `^queue_0`, Type: "sum"})
	s, _ := newTestScraper(t, cfg, nic)

	got := statisticPoints(t, s.scrape(time.Unix(2000, 0)), pmetric.MetricTypeSum)

	assert.Equal(t, map[string]int64{"eth0/queue_0_tx_cnt": 4}, got)
}

func TestScrapeZeroMaxStatisticsFallsBackToTheDefault(t *testing.T) {
	cfg := statisticsConfig(Statistic{Match: `^queue_`, Type: "sum"})
	cfg.MaxStatistics = 0
	s, _ := newTestScraper(t, cfg, queueNIC(250))

	got := statisticPoints(t, s.scrape(time.Unix(2000, 0)), pmetric.MetricTypeSum)

	assert.Len(t, got, defaultMaxStatistics)
}
