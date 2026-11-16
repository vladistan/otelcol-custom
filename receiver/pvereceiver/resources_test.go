// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package pvereceiver

import (
	"context"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.uber.org/zap"
)

const resourcesFixture = `{"data":[
 {"id":"node/pve1","type":"node","node":"pve1","status":"online","cpu":0.12,"maxcpu":8,"mem":4294967296,"maxmem":17179869184,"disk":1000,"maxdisk":2000,"uptime":86400},
 {"id":"node/pve2","type":"node","node":"pve2","status":"offline"},
 {"id":"qemu/100","type":"qemu","node":"pve1","vmid":100,"name":"web","status":"running","cpu":0.05,"maxcpu":2,"mem":1000,"maxmem":2000,"disk":0,"maxdisk":3000,"netin":11,"netout":22,"diskread":33,"diskwrite":44,"uptime":3600,"tags":"prod;web","hastate":"started"},
 {"id":"qemu/101","type":"qemu","node":"pve1","vmid":101,"name":"old","status":"stopped","maxcpu":1,"maxmem":1073741824,"maxdisk":4000},
 {"id":"qemu/9000","type":"qemu","node":"pve1","vmid":9000,"name":"tmpl","status":"stopped","template":1,"maxcpu":1,"maxmem":1,"maxdisk":1},
 {"id":"lxc/200","type":"lxc","node":"pve1","vmid":200,"name":"ct","status":"running","cpu":0.01,"maxcpu":1,"mem":5,"maxmem":6,"disk":7,"maxdisk":8,"netin":1,"netout":2,"diskread":3,"diskwrite":4,"uptime":100},
 {"id":"qemu/300","type":"qemu","node":"pve2","vmid":300,"name":"stale","status":"unknown"},
 {"id":"storage/pve1/local","type":"storage","node":"pve1","storage":"local","status":"available","disk":100,"maxdisk":1000,"shared":0,"content":"iso,vztmpl","plugintype":"dir"},
 {"id":"storage/pve1/ceph","type":"storage","node":"pve1","storage":"ceph","status":"available","disk":200,"maxdisk":2000,"shared":1,"content":"images","plugintype":"rbd"},
 {"id":"sdn/pve1/localnetwork","type":"sdn","node":"pve1","status":"ok","sdn":"localnetwork"},
 {"id":"pool/prod","type":"pool","poolid":"prod"}
]}`

const (
	clusterStatusFixture = `{"data":[{"type":"cluster","name":"lab","nodes":2,"quorate":1},{"type":"node","name":"pve1","nodeid":1,"online":1,"level":"","local":1},{"type":"node","name":"pve2","nodeid":2,"online":0,"level":""}]}`
	versionFixture       = `{"data":{"version":"8.0.3","release":"8.0","repoid":"bbf3993334bfa916"}}`
)

type point struct {
	name  string
	attrs map[string]string
	value float64
	unit  string
	kind  pmetric.MetricType
	mono  bool
	start time.Time
}

func flatten(md pmetric.Metrics) []point {
	var out []point
	rms := md.ResourceMetrics()
	for i := 0; i < rms.Len(); i++ {
		sms := rms.At(i).ScopeMetrics()
		for j := 0; j < sms.Len(); j++ {
			ms := sms.At(j).Metrics()
			for k := 0; k < ms.Len(); k++ {
				m := ms.At(k)
				var dps pmetric.NumberDataPointSlice
				mono := false
				switch m.Type() {
				case pmetric.MetricTypeGauge:
					dps = m.Gauge().DataPoints()
				case pmetric.MetricTypeSum:
					dps = m.Sum().DataPoints()
					mono = m.Sum().IsMonotonic()
				default:
					continue
				}
				for d := 0; d < dps.Len(); d++ {
					dp := dps.At(d)
					attrs := map[string]string{}
					dp.Attributes().Range(func(key string, v pcommon.Value) bool {
						attrs[key] = v.AsString()
						return true
					})
					v := dp.DoubleValue()
					if dp.ValueType() == pmetric.NumberDataPointValueTypeInt {
						v = float64(dp.IntValue())
					}
					out = append(out, point{name: m.Name(), attrs: attrs, value: v, unit: m.Unit(), kind: m.Type(), mono: mono, start: dp.StartTimestamp().AsTime()})
				}
			}
		}
	}
	return out
}

func find(pts []point, name string, kv ...string) []point {
	var out []point
	for _, p := range pts {
		if p.name != name {
			continue
		}
		match := true
		for i := 0; i+1 < len(kv); i += 2 {
			if p.attrs[kv[i]] != kv[i+1] {
				match = false
			}
		}
		if match {
			out = append(out, p)
		}
	}
	return out
}

func names(pts []point) []string {
	set := map[string]bool{}
	for _, p := range pts {
		set[p.name] = true
	}
	var out []string
	for n := range set {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func resourcesFake(t *testing.T) *fakePVE {
	t.Helper()
	f := newFakePVE(t, false)
	f.ok("/api2/json/cluster/resources", resourcesFixture)
	f.ok("/api2/json/cluster/status", clusterStatusFixture)
	f.ok("/api2/json/version", versionFixture)
	return f
}

func newResources(t *testing.T, f *fakePVE, mutate func(*Config)) *resourcesScraper {
	t.Helper()
	cfg := validConfig()
	cfg.BaseURLs = []string{f.url()}
	cfg.Resources.Timeout = time.Second
	if mutate != nil {
		mutate(cfg)
	}
	c, err := newAPIClient(cfg, cfg.Resources.TokenID, cfg.Resources.TokenSecret)
	require.NoError(t, err)
	return newResourcesScraper(zap.NewNop(), cfg, c, time.Unix(1000, 0))
}

func scrapeResources(t *testing.T, s *resourcesScraper) []point {
	t.Helper()
	md, err := s.scrape(context.Background(), time.Unix(2000, 0))
	require.NoError(t, err)
	return flatten(md)
}

func TestFixtureGivesTheElevenResourceMetrics(t *testing.T) {
	pts := scrapeResources(t, newResources(t, resourcesFake(t), nil))

	for _, name := range []string{
		"pve.resource.up", "pve.resource.cpu.utilization", "pve.resource.cpu.limit",
		"pve.resource.memory.usage", "pve.resource.memory.limit", "pve.resource.disk.usage",
		"pve.resource.disk.limit", "pve.resource.network.io", "pve.resource.disk.io",
		"pve.resource.uptime", "pve.storage.shared",
	} {
		assert.NotEmpty(t, find(pts, name), name)
	}
}

func TestUnitsTypesAndAttributesOfTheResourceMetrics(t *testing.T) {
	pts := scrapeResources(t, newResources(t, resourcesFake(t), nil))

	up := find(pts, "pve.resource.up", "pve.resource.id", "qemu/100")
	require.Len(t, up, 1)
	assert.Equal(t, "1", up[0].unit)
	assert.Equal(t, "qemu", up[0].attrs["pve.resource.type"])
	assert.Equal(t, "pve1", up[0].attrs["pve.node"])
	assert.Equal(t, "running", up[0].attrs["status"])

	cpu := find(pts, "pve.resource.cpu.utilization", "pve.resource.id", "qemu/100")
	require.Len(t, cpu, 1)
	assert.InDelta(t, 0.05, cpu[0].value, 1e-9)
	assert.Equal(t, "1", cpu[0].unit)
	limit := find(pts, "pve.resource.cpu.limit", "pve.resource.id", "qemu/100")
	assert.Equal(t, "{cpu}", limit[0].unit)
	assert.InDelta(t, 2, limit[0].value, 1e-9)

	mem := find(pts, "pve.resource.memory.usage", "pve.resource.id", "qemu/100")
	assert.Equal(t, "By", mem[0].unit)
	assert.InDelta(t, 1000, mem[0].value, 1e-9)
	assert.Equal(t, "s", find(pts, "pve.resource.uptime", "pve.resource.id", "qemu/100")[0].unit)

	netIn := find(pts, "pve.resource.network.io", "pve.resource.id", "qemu/100", "network.io.direction", "receive")
	netOut := find(pts, "pve.resource.network.io", "pve.resource.id", "qemu/100", "network.io.direction", "transmit")
	require.Len(t, netIn, 1)
	require.Len(t, netOut, 1)
	assert.InDelta(t, 11, netIn[0].value, 1e-9)
	assert.InDelta(t, 22, netOut[0].value, 1e-9)
	assert.True(t, netIn[0].mono)
	assert.Equal(t, pmetric.MetricTypeSum, netIn[0].kind)
	assert.Equal(t, "By", netIn[0].unit)

	diskRead := find(pts, "pve.resource.disk.io", "pve.resource.id", "qemu/100", "disk.io.direction", "read")
	diskWrite := find(pts, "pve.resource.disk.io", "pve.resource.id", "qemu/100", "disk.io.direction", "write")
	assert.InDelta(t, 33, diskRead[0].value, 1e-9)
	assert.InDelta(t, 44, diskWrite[0].value, 1e-9)
}

func TestStatusMapsToUp(t *testing.T) {
	pts := scrapeResources(t, newResources(t, resourcesFake(t), nil))

	for id, want := range map[string]float64{
		"node/pve1": 1, "node/pve2": 0, "qemu/100": 1, "qemu/101": 0, "lxc/200": 1,
		"qemu/300": 0, "storage/pve1/local": 1,
	} {
		got := find(pts, "pve.resource.up", "pve.resource.id", id)
		require.Len(t, got, 1, id)
		assert.InDelta(t, want, got[0].value, 1e-9, id)
	}
}

func TestOfflineNodeKeepsTheOthersAndMarksItsGuestsUnknown(t *testing.T) {
	pts := scrapeResources(t, newResources(t, resourcesFake(t), nil))

	stale := find(pts, "pve.resource.up", "pve.resource.id", "qemu/300")
	require.Len(t, stale, 1)
	assert.Equal(t, "unknown", stale[0].attrs["status"])
	assert.Equal(t, "pve2", stale[0].attrs["pve.node"])
	assert.NotEmpty(t, find(pts, "pve.resource.cpu.utilization", "pve.resource.id", "qemu/100"), "other nodes keep their data")
}

func TestStorageMetricsAndInfo(t *testing.T) {
	pts := scrapeResources(t, newResources(t, resourcesFake(t), nil))

	shared := find(pts, "pve.storage.shared", "pve.resource.id", "storage/pve1/ceph")
	require.Len(t, shared, 1)
	assert.InDelta(t, 1, shared[0].value, 1e-9)
	local := find(pts, "pve.storage.shared", "pve.resource.id", "storage/pve1/local")
	assert.InDelta(t, 0, local[0].value, 1e-9)
	info := find(pts, "pve.storage.info", "storage", "local")
	require.Len(t, info, 1)
	assert.Equal(t, "dir", info[0].attrs["plugintype"])
	assert.Equal(t, "iso,vztmpl", info[0].attrs["content"])
	assert.Len(t, find(pts, "pve.storage.info"), 2)
}

func TestFiveInfoGaugesWithHAStateOnlyForManagedGuests(t *testing.T) {
	pts := scrapeResources(t, newResources(t, resourcesFake(t), nil))

	guests := find(pts, "pve.guest.info")
	assert.Len(t, guests, 5)
	web := find(pts, "pve.guest.info", "name", "web")
	require.Len(t, web, 1)
	assert.Equal(t, "qemu", web[0].attrs["type"])
	assert.Equal(t, "prod;web", web[0].attrs["tags"])
	assert.Equal(t, "true", find(pts, "pve.guest.info", "name", "tmpl")[0].attrs["template"])

	nodes := find(pts, "pve.node.info")
	require.Len(t, nodes, 2)
	assert.Equal(t, "1", find(pts, "pve.node.info", "name", "pve1")[0].attrs["nodeid"])
	version := find(pts, "pve.version.info")
	require.Len(t, version, 1)
	assert.Equal(t, "8.0.3", version[0].attrs["version"])
	assert.Equal(t, "bbf3993334bfa916", version[0].attrs["repoid"])

	ha := find(pts, "pve.resource.ha.state")
	require.Len(t, ha, 1, "only the HA-managed guest")
	assert.Equal(t, "started", ha[0].attrs["state"])
	assert.Equal(t, "qemu/100", ha[0].attrs["pve.resource.id"])
}

func TestEveryMetricNameStartsWithPve(t *testing.T) {
	for _, name := range names(scrapeResources(t, newResources(t, resourcesFake(t), nil))) {
		assert.True(t, strings.HasPrefix(name, "pve."), name)
		assert.False(t, strings.HasPrefix(name, "pve_"), name)
	}
}

func TestOneIntervalMakesOneOfEachRequestAndNeverTwoInFlight(t *testing.T) {
	f := resourcesFake(t)
	s := newResources(t, f, nil)

	scrapeResources(t, s)

	assert.Equal(t, 1, f.countOf("/api2/json/cluster/resources"))
	assert.Equal(t, 1, f.countOf("/api2/json/cluster/status"))
	assert.Equal(t, 1, f.countOf("/api2/json/version"))
	assert.Len(t, f.hitList(), 3)
	assert.Equal(t, 1, f.maxConcurrent())
}

func TestSlowResourcesAnswerIsCutAtTheTimeout(t *testing.T) {
	f := resourcesFake(t)
	f.delay = 400 * time.Millisecond
	s := newResources(t, f, func(c *Config) { c.Resources.Timeout = 50 * time.Millisecond })

	start := time.Now()
	_, err := s.scrape(context.Background(), time.Now())

	require.Error(t, err)
	assert.Less(t, time.Since(start), 300*time.Millisecond)
}

func TestSecondURLGivesTheDataAfterA503(t *testing.T) {
	bad := newFakePVE(t, false)
	for _, p := range []string{"/api2/json/cluster/resources", "/api2/json/cluster/status", "/api2/json/version"} {
		bad.set(p, http.StatusServiceUnavailable, "down")
	}
	good := resourcesFake(t)
	s := newResources(t, good, func(c *Config) { c.BaseURLs = []string{bad.url(), good.url()} })

	pts := scrapeResources(t, s)

	assert.NotEmpty(t, find(pts, "pve.resource.up"))
}

func TestAFailingStatusCallKeepsTheResourceMetricsAndReturnsTheError(t *testing.T) {
	f := resourcesFake(t)
	f.set("/api2/json/cluster/status", http.StatusInternalServerError, "boom")
	s := newResources(t, f, nil)

	md, err := s.scrape(context.Background(), time.Unix(2000, 0))

	require.Error(t, err)
	pts := flatten(md)
	assert.NotEmpty(t, find(pts, "pve.resource.up"))
	assert.Empty(t, find(pts, "pve.node.info"))
}

func TestResourcesFailureReturnsNoMetrics(t *testing.T) {
	f := resourcesFake(t)
	f.set("/api2/json/cluster/resources", http.StatusUnauthorized, "no ticket")
	s := newResources(t, f, nil)

	md, err := s.scrape(context.Background(), time.Unix(2000, 0))

	require.Error(t, err)
	assert.Zero(t, md.MetricCount())
}

func TestDefaultConfigMakesNoCallToTheOptionalPaths(t *testing.T) {
	f := resourcesFake(t)
	scrapeResources(t, newResources(t, f, nil))

	for _, h := range f.hitList() {
		assert.NotContains(t, h, "subscription")
		assert.NotContains(t, h, "replication")
		assert.NotContains(t, h, "backup-info")
		assert.NotContains(t, h, "/config")
	}
}

func TestEachOptionalKeyMakesExactlyItsCall(t *testing.T) {
	cases := map[string]struct {
		set   func(*Config)
		paths []string
		body  string
		want  string
	}{
		"subscription": {
			set:   func(c *Config) { c.Resources.Subscription = true },
			paths: []string{"/api2/json/nodes/pve1/subscription"},
			body:  `{"data":{"status":"Active","level":"c"}}`,
			want:  "pve.subscription.status",
		},
		"replication": {
			set:   func(c *Config) { c.Resources.Replication = true },
			paths: []string{"/api2/json/cluster/replication"},
			body:  `{"data":[{"id":"100-0","guest":100,"source":"pve1","target":"pve2"}]}`,
			want:  "pve.replication.job",
		},
		"backup_info": {
			set:   func(c *Config) { c.Resources.BackupInfo = true },
			paths: []string{"/api2/json/cluster/backup-info/not-backed-up"},
			body:  `{"data":[{"vmid":101,"name":"old","type":"qemu"}]}`,
			want:  "pve.guest.not_backed_up",
		},
		"guest_config": {
			set:   func(c *Config) { c.Resources.GuestConfig = true },
			paths: []string{"/api2/json/nodes/pve1/qemu/100/config", "/api2/json/nodes/pve1/qemu/101/config", "/api2/json/nodes/pve1/qemu/9000/config", "/api2/json/nodes/pve1/lxc/200/config"},
			body:  `{"data":{"onboot":1}}`,
			want:  "pve.guest.onboot",
		},
	}
	for name, tc := range cases {
		f := resourcesFake(t)
		for _, p := range tc.paths {
			f.ok(p, tc.body)
		}
		pts := scrapeResources(t, newResources(t, f, tc.set))

		hits := f.hitList()
		for _, p := range tc.paths {
			assert.Contains(t, hits, p, name)
		}
		assert.NotEmpty(t, find(pts, tc.want), name)
		extra := 0
		for _, h := range hits {
			if strings.Contains(h, "subscription") || strings.Contains(h, "replication") || strings.Contains(h, "backup-info") || strings.HasSuffix(h, "/config") {
				extra++
			}
		}
		assert.Len(t, tc.paths, extra, name+": no other optional call")
	}
}

func TestOptionalCallFailureDoesNotDropTheBaseMetrics(t *testing.T) {
	f := resourcesFake(t)
	cfgSet := func(c *Config) { c.Resources.Subscription = true }
	s := newResources(t, f, cfgSet)

	md, err := s.scrape(context.Background(), time.Unix(2000, 0))

	require.Error(t, err, "the 404 of the optional call is reported")
	assert.NotEmpty(t, find(flatten(md), "pve.resource.up"))
}

func TestOnlyNodeGuestAndStorageEntriesAreReported(t *testing.T) {
	pts := scrapeResources(t, newResources(t, resourcesFake(t), nil))

	for _, p := range find(pts, "pve.resource.up") {
		assert.Contains(t, []string{"node", "qemu", "lxc", "storage"}, p.attrs["pve.resource.type"], p.attrs["pve.resource.id"])
	}
	assert.Empty(t, find(pts, "pve.resource.up", "pve.resource.id", "sdn/pve1/localnetwork"), "an SDN zone is not a node")
	assert.Empty(t, find(pts, "pve.resource.up", "pve.resource.id", "pool/prod"))
	assert.Len(t, find(pts, "pve.resource.up"), 9, "two nodes, five guests and two storages: nothing else")
}

func counterStarts(pts []point, id string) []time.Time {
	var out []time.Time
	for _, p := range find(pts, "pve.resource.network.io", "pve.resource.id", id) {
		out = append(out, p.start)
	}
	return out
}

func TestCounterStartStaysWhileUptimeGrowsAndMovesWhenTheGuestRestarts(t *testing.T) {
	f := resourcesFake(t)
	s := newResources(t, f, nil)
	body := func(uptime int) string {
		return `{"data":[{"id":"qemu/100","type":"qemu","node":"pve1","vmid":100,"name":"web","status":"running","netin":1,"netout":2,"uptime":` + strconv.Itoa(uptime) + `}]}`
	}
	scrapeAt := func(now time.Time, uptime int) []time.Time {
		f.ok("/api2/json/cluster/resources", body(uptime))
		md, err := s.scrape(context.Background(), now)
		require.NoError(t, err)
		return counterStarts(flatten(md), "qemu/100")
	}

	first := scrapeAt(time.Unix(10000, 0), 3600)
	second := scrapeAt(time.Unix(10060, 0), 3665) // the uptime answer is rounded, so it can be a few seconds off
	third := scrapeAt(time.Unix(10120, 0), 30)    // the guest restarted

	require.Len(t, first, 2)
	assert.Equal(t, time.Unix(6400, 0).UTC(), first[0].UTC())
	assert.Equal(t, first, second, "the start does not creep with the rounded uptime")
	assert.Equal(t, time.Unix(10090, 0).UTC(), third[0].UTC(), "a smaller uptime is a restart: the start moves")
}

func TestStartStateOfAGoneResourceIsDropped(t *testing.T) {
	f := resourcesFake(t)
	s := newResources(t, f, nil)
	scrapeResources(t, s)
	require.NotEmpty(t, s.starts)

	f.ok("/api2/json/cluster/resources", `{"data":[]}`)
	_, err := s.scrape(context.Background(), time.Unix(3000, 0))

	require.NoError(t, err)
	assert.Empty(t, s.starts)
}
