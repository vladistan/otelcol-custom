// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package ethtoolreceiver

import (
	"errors"
	"slices"
	"strings"
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

var allowanceNames = []string{
	"bw_in_allowance_exceeded",
	"bw_out_allowance_exceeded",
	"pps_allowance_exceeded",
	"conntrack_allowance_exceeded",
	"linklocal_allowance_exceeded",
}

// fakeNIC answers the nicReader calls from tables; err* maps force an error per interface.
type fakeNIC struct {
	ifaces      []string
	listErr     error
	drivers     map[string]string
	stats       map[string][]stat
	driverErr   map[string]error
	countErr    map[string]error
	valuesCount map[string]int // when set, statValues returns this many values
	namesErr    map[string]error
	boot        time.Time
	bootErr     error
	renamed     map[string]bool // statNames reverses its answer from the second call on
	nameCalls   map[string]int
	valuesErr   map[string]error
}

type stat struct {
	name  string
	value uint64
}

func (f *fakeNIC) interfaces() ([]string, error) { return f.ifaces, f.listErr }

func (f *fakeNIC) driver(iface string) (string, error) {
	if err := f.driverErr[iface]; err != nil {
		return "", err
	}
	return f.drivers[iface], nil
}

func (f *fakeNIC) statCount(iface string) (int, error) {
	if err := f.countErr[iface]; err != nil {
		return 0, err
	}
	return len(f.stats[iface]), nil
}

func (f *fakeNIC) bootTime() (time.Time, error) { return f.boot, f.bootErr }

func (f *fakeNIC) statNames(iface string, _ int) ([]string, error) {
	if err := f.namesErr[iface]; err != nil {
		return nil, err
	}
	if f.nameCalls == nil {
		f.nameCalls = map[string]int{}
	}
	f.nameCalls[iface]++
	names := make([]string, 0, len(f.stats[iface]))
	for _, s := range f.stats[iface] {
		names = append(names, s.name)
	}
	if f.renamed[iface] && f.nameCalls[iface] > 1 {
		slices.Reverse(names)
	}
	return names, nil
}

func (f *fakeNIC) statValues(iface string, _ int) ([]uint64, error) {
	if err := f.valuesErr[iface]; err != nil {
		return nil, err
	}
	n := len(f.stats[iface])
	if c, ok := f.valuesCount[iface]; ok {
		n = c
	}
	values := make([]uint64, n)
	for i := range values {
		if i < len(f.stats[iface]) {
			values[i] = f.stats[iface][i].value
		}
	}
	return values, nil
}

func (f *fakeNIC) close() error { return nil }

func enaStats(base uint64) []stat {
	out := make([]stat, 0, len(allowanceNames))
	for i, n := range allowanceNames {
		out = append(out, stat{n, base + uint64(i)})
	}
	return out
}

func enaNIC() *fakeNIC {
	return &fakeNIC{
		ifaces:  []string{"eth0"},
		drivers: map[string]string{"eth0": "ena"},
		stats:   map[string][]stat{"eth0": enaStats(10)},
	}
}

func testConfig() *Config {
	return &Config{CollectionInterval: time.Minute, Drivers: []string{"ena"}}
}

func newTestScraper(t *testing.T, cfg *Config, nic nicReader) (*scraper, *observer.ObservedLogs) {
	t.Helper()
	core, logs := observer.New(zapcore.DebugLevel)
	return newScraper(zap.New(core), cfg, nic, time.Unix(1000, 0)), logs
}

// points flattens the allowance sum into "interface/allowance" -> value.
func points(t *testing.T, md pmetric.Metrics) map[string]int64 {
	t.Helper()
	out := map[string]int64{}
	for i := 0; i < md.ResourceMetrics().Len(); i++ {
		sms := md.ResourceMetrics().At(i).ScopeMetrics()
		for j := 0; j < sms.Len(); j++ {
			ms := sms.At(j).Metrics()
			for k := 0; k < ms.Len(); k++ {
				m := ms.At(k)
				require.Equal(t, "network.allowance.exceeded", m.Name())
				dps := m.Sum().DataPoints()
				for d := 0; d < dps.Len(); d++ {
					dp := dps.At(d)
					iface, ok := dp.Attributes().Get("network.interface.name")
					require.True(t, ok, "point without network.interface.name")
					allow, ok := dp.Attributes().Get("allowance")
					require.True(t, ok, "point without allowance")
					out[iface.Str()+"/"+allow.Str()] = dp.IntValue()
				}
			}
		}
	}
	return out
}

func countLevel(logs *observer.ObservedLogs, level zapcore.Level) int {
	return logs.FilterLevelExact(level).Len()
}

func TestScrapeEnaLikeStringsYieldFiveSeriesForOneInterface(t *testing.T) {
	s, _ := newTestScraper(t, testConfig(), enaNIC())

	got := points(t, s.scrape(time.Unix(2000, 0)))

	assert.Equal(t, map[string]int64{
		"eth0/bandwidth_in":  10,
		"eth0/bandwidth_out": 11,
		"eth0/pps":           12,
		"eth0/conntrack":     13,
		"eth0/linklocal":     14,
	}, got)
}

func TestScrapeMetricIsAMonotonicCumulativeSumInPackets(t *testing.T) {
	s, _ := newTestScraper(t, testConfig(), enaNIC())

	md := s.scrape(time.Unix(2000, 0))

	require.Equal(t, 1, md.MetricCount())
	m := md.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics().At(0)
	assert.Equal(t, "network.allowance.exceeded", m.Name())
	assert.Equal(t, "{packet}", m.Unit())
	require.Equal(t, pmetric.MetricTypeSum, m.Type())
	assert.True(t, m.Sum().IsMonotonic())
	assert.Equal(t, pmetric.AggregationTemporalityCumulative, m.Sum().AggregationTemporality())
}

func TestScrapePointTimesAreReceiverStartAndScrapeTime(t *testing.T) {
	s, _ := newTestScraper(t, testConfig(), enaNIC())

	md := s.scrape(time.Unix(2000, 0))

	dp := md.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics().At(0).Sum().DataPoints().At(0)
	assert.Equal(t, pcommon.NewTimestampFromTime(time.Unix(1000, 0)), dp.StartTimestamp())
	assert.Equal(t, pcommon.NewTimestampFromTime(time.Unix(2000, 0)), dp.Timestamp())
}

func TestScrapeDriversAndInterfacesSelectTheInterfaces(t *testing.T) {
	tests := map[string]struct {
		drivers    []string
		interfaces []string
		want       []string
	}{
		"driver only":                  {[]string{"ena"}, nil, []string{"eth0", "eth2"}},
		"interface names narrow":       {[]string{"ena"}, []string{"eth2"}, []string{"eth2"}},
		"another driver":               {[]string{"mlx5_core"}, nil, []string{"eth1"}},
		"two drivers":                  {[]string{"ena", "mlx5_core"}, []string{"eth0", "eth1"}, []string{"eth0", "eth1"}},
		"named interface wrong driver": {[]string{"ena"}, []string{"eth1"}, nil},
		"named interface absent":       {[]string{"ena"}, []string{"eth9"}, nil},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			nic := &fakeNIC{
				ifaces:  []string{"eth0", "eth1", "eth2"},
				drivers: map[string]string{"eth0": "ena", "eth1": "mlx5_core", "eth2": "ena"},
				stats:   map[string][]stat{"eth0": enaStats(1), "eth1": enaStats(1), "eth2": enaStats(1)},
			}
			cfg := testConfig()
			cfg.Drivers, cfg.Interfaces = tt.drivers, tt.interfaces
			s, _ := newTestScraper(t, cfg, nic)

			got := map[string]bool{}
			for k := range points(t, s.scrape(time.Unix(2000, 0))) {
				got[strings.SplitN(k, "/", 2)[0]] = true
			}

			want := map[string]bool{}
			for _, n := range tt.want {
				want[n] = true
			}
			assert.Equal(t, want, got)
		})
	}
}

func TestScrapeOtherStatisticsEmitNothing(t *testing.T) {
	nic := enaNIC()
	nic.stats["eth0"] = append(enaStats(10),
		stat{"queue_0_tx_cnt", 99},
		stat{"conntrack_allowance_available", 98},
		stat{"tx_timeout", 97})
	s, _ := newTestScraper(t, testConfig(), nic)

	got := points(t, s.scrape(time.Unix(2000, 0)))

	assert.Equal(t, map[string]int64{
		"eth0/bandwidth_in":  10,
		"eth0/bandwidth_out": 11,
		"eth0/pps":           12,
		"eth0/conntrack":     13,
		"eth0/linklocal":     14,
	}, got)
}

func TestScrapeUnsupportedAndRemovedInterfacesAreSkippedWithoutWarning(t *testing.T) {
	for name, err := range map[string]error{
		"EOPNOTSUPP": errNotSupported,
		"ENODEV":     errNoDevice,
	} {
		t.Run(name, func(t *testing.T) {
			nic := enaNIC()
			nic.ifaces = []string{"veth0", "eth0"}
			nic.driverErr = map[string]error{"veth0": err}
			s, logs := newTestScraper(t, testConfig(), nic)

			got := points(t, s.scrape(time.Unix(2000, 0)))

			assert.Len(t, got, 5, "the ena interface still reports")
			assert.Zero(t, countLevel(logs, zapcore.WarnLevel))
			assert.Zero(t, countLevel(logs, zapcore.ErrorLevel))
		})
	}
}

func TestScrapeInterfaceRemovedDuringStatReadIsSkippedWithoutWarning(t *testing.T) {
	nic := enaNIC()
	nic.countErr = map[string]error{"eth0": errNoDevice}
	s, logs := newTestScraper(t, testConfig(), nic)

	md := s.scrape(time.Unix(2000, 0))

	assert.Zero(t, md.MetricCount())
	assert.Zero(t, countLevel(logs, zapcore.WarnLevel))
}

func TestScrapeDriverWithoutTheCountersWarnsOnceAndEmitsNothing(t *testing.T) {
	nic := enaNIC()
	nic.stats["eth0"] = []stat{{"tx_timeout", 1}, {"queue_0_tx_cnt", 2}}
	s, logs := newTestScraper(t, testConfig(), nic)

	first := s.scrape(time.Unix(2000, 0))
	second := s.scrape(time.Unix(2060, 0))

	assert.Zero(t, first.MetricCount())
	assert.Zero(t, second.MetricCount())
	require.Equal(t, 1, countLevel(logs, zapcore.WarnLevel))
	msg := logs.FilterLevelExact(zapcore.WarnLevel).All()[0]
	assert.Equal(t, "eth0", msg.ContextMap()["interface"])
}

func TestScrapeDriverWithSomeCountersEmitsThemAndWarnsOnceForTheRest(t *testing.T) {
	nic := enaNIC()
	nic.stats["eth0"] = []stat{{"bw_in_allowance_exceeded", 5}, {"pps_allowance_exceeded", 6}}
	s, logs := newTestScraper(t, testConfig(), nic)

	got := points(t, s.scrape(time.Unix(2000, 0)))
	s.scrape(time.Unix(2060, 0))

	assert.Equal(t, map[string]int64{"eth0/bandwidth_in": 5, "eth0/pps": 6}, got)
	assert.Equal(t, 1, countLevel(logs, zapcore.WarnLevel))
}

func TestScrapePermissionDeniedWarnsOnceNamingInterfaceAndTheSecurityProfile(t *testing.T) {
	nic := enaNIC()
	nic.driverErr = map[string]error{"eth0": errPermission}
	s, logs := newTestScraper(t, testConfig(), nic)

	s.scrape(time.Unix(2000, 0))
	s.scrape(time.Unix(2060, 0))

	warns := logs.FilterLevelExact(zapcore.WarnLevel).All()
	require.Len(t, warns, 1)
	assert.Equal(t, "eth0", warns[0].ContextMap()["interface"])
	assert.Contains(t, warns[0].Message, "security profile")
	assert.NotContains(t, warns[0].Message, "CAP_NET_ADMIN", "reads need no capability")
}

func TestScrapePermissionWarningRepeatsAfterTheInterfaceWorksAgain(t *testing.T) {
	nic := enaNIC()
	nic.driverErr = map[string]error{"eth0": errPermission}
	s, logs := newTestScraper(t, testConfig(), nic)
	s.scrape(time.Unix(2000, 0))

	nic.driverErr = nil
	require.Len(t, points(t, s.scrape(time.Unix(2060, 0))), 5)
	nic.driverErr = map[string]error{"eth0": errPermission}
	s.scrape(time.Unix(2120, 0))

	assert.Equal(t, 2, countLevel(logs, zapcore.WarnLevel))
}

func TestScrapeStatisticCountChangeDropsThatReadAndTheNextScrapeEmits(t *testing.T) {
	nic := enaNIC()
	nic.valuesCount = map[string]int{"eth0": len(allowanceNames) + 3}
	s, logs := newTestScraper(t, testConfig(), nic)

	dropped := s.scrape(time.Unix(2000, 0))
	nic.valuesCount = nil
	next := s.scrape(time.Unix(2060, 0))

	assert.Zero(t, dropped.MetricCount())
	assert.Len(t, points(t, next), 5)
	assert.Zero(t, countLevel(logs, zapcore.WarnLevel))
}

func TestScrapeCountChangeReportedByTheReaderDropsThatReadToo(t *testing.T) {
	nic := enaNIC()
	nic.countErr = map[string]error{"eth0": errCountChanged}
	s, _ := newTestScraper(t, testConfig(), nic)

	assert.Zero(t, s.scrape(time.Unix(2000, 0)).MetricCount())
}

func TestScrapeNoMatchingInterfaceLogsOneInfoLineAndEmitsNothing(t *testing.T) {
	nic := enaNIC()
	nic.drivers["eth0"] = "virtio_net"
	s, logs := newTestScraper(t, testConfig(), nic)

	first := s.scrape(time.Unix(2000, 0))
	second := s.scrape(time.Unix(2060, 0))

	assert.Zero(t, first.MetricCount())
	assert.Zero(t, second.MetricCount())
	assert.Equal(t, 1, countLevel(logs, zapcore.InfoLevel))
	assert.Zero(t, countLevel(logs, zapcore.WarnLevel))
}

func TestScrapeNoInterfacesAtAllIsTheSameAsNoMatch(t *testing.T) {
	nic := &fakeNIC{}
	s, logs := newTestScraper(t, testConfig(), nic)

	assert.Zero(t, s.scrape(time.Unix(2000, 0)).MetricCount())
	assert.Equal(t, 1, countLevel(logs, zapcore.InfoLevel))
}

func TestScrapeNoMatchLineRepeatsAfterAMatchWasSeen(t *testing.T) {
	nic := enaNIC()
	s, logs := newTestScraper(t, testConfig(), nic)
	s.scrape(time.Unix(2000, 0))

	nic.ifaces = nil
	s.scrape(time.Unix(2060, 0))

	assert.Equal(t, 1, countLevel(logs, zapcore.InfoLevel))
}

func TestScrapeListingFailureWarnsAndEmitsNothing(t *testing.T) {
	nic := enaNIC()
	nic.listErr = errors.New("netlink down")
	s, logs := newTestScraper(t, testConfig(), nic)

	assert.Zero(t, s.scrape(time.Unix(2000, 0)).MetricCount())
	assert.Equal(t, 1, countLevel(logs, zapcore.WarnLevel))
}

func TestScrapeUnexpectedErrorOnOneInterfaceDoesNotStopTheOthers(t *testing.T) {
	nic := enaNIC()
	nic.ifaces = []string{"eth1", "eth0"}
	nic.drivers["eth1"] = "ena"
	nic.driverErr = map[string]error{"eth1": errors.New("boom")}
	s, logs := newTestScraper(t, testConfig(), nic)

	got := points(t, s.scrape(time.Unix(2000, 0)))

	assert.Len(t, got, 5)
	assert.Equal(t, 1, countLevel(logs, zapcore.WarnLevel))
}

func TestScrapeFailureInTheNamesOrValuesCallSkipsTheInterface(t *testing.T) {
	tests := map[string]func(*fakeNIC){
		"names":  func(n *fakeNIC) { n.namesErr = map[string]error{"eth0": errNoDevice} },
		"values": func(n *fakeNIC) { n.valuesErr = map[string]error{"eth0": errCountChanged} },
	}
	for name, setup := range tests {
		t.Run(name, func(t *testing.T) {
			nic := enaNIC()
			setup(nic)
			s, logs := newTestScraper(t, testConfig(), nic)

			assert.Zero(t, s.scrape(time.Unix(2000, 0)).MetricCount())
			assert.Zero(t, countLevel(logs, zapcore.WarnLevel))
		})
	}
}

func TestScrapeNamesThatChangeBetweenTheCallsDropTheRead(t *testing.T) {
	nic := enaNIC()
	nic.renamed = map[string]bool{"eth0": true}
	s, logs := newTestScraper(t, testConfig(), nic)

	got := s.scrape(time.Unix(2000, 0))

	assert.Zero(t, got.MetricCount(), "names read before and after the values differ")
	assert.Zero(t, countLevel(logs, zapcore.WarnLevel))
}
