// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package pvereceiver

import (
	"context"
	"errors"
	"math/rand"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

const (
	qemuList = `{"data":[{"vmid":100,"name":"web","status":"running"},{"vmid":101,"name":"old","status":"stopped"},{"vmid":102,"name":"noagent","status":"running"}]}`
	lxcList  = `{"data":[{"vmid":200,"name":"ct","status":"running"},{"vmid":201,"name":"off","status":"stopped"}]}`
	prefix   = "/api2/json/nodes/pve1"
)

func agentBody(result string) string { return `{"data":{"result":` + result + `}}` }

func guestsFake(t *testing.T) *fakePVE {
	t.Helper()
	f := newFakePVE(t, false)
	f.ok(prefix+"/qemu", qemuList)
	f.ok(prefix+"/lxc", lxcList)
	f.ok(prefix+"/qemu/100/agent/get-osinfo", agentBody(`{"name":"Debian GNU/Linux","version":"12 (bookworm)","pretty-name":"Debian GNU/Linux 12 (bookworm)"}`))
	f.ok(prefix+"/qemu/100/agent/get-host-name", agentBody(`{"host-name":"web01"}`))
	f.ok(prefix+"/qemu/100/agent/network-get-interfaces", agentBody(`[{"name":"lo","ip-addresses":[{"ip-address":"127.0.0.1","ip-address-type":"ipv4","prefix":8}]},{"name":"eth0","ip-addresses":[{"ip-address":"10.0.0.5","ip-address-type":"ipv4","prefix":24},{"ip-address":"fe80::1","ip-address-type":"ipv6","prefix":64}]}]`))
	f.ok(prefix+"/qemu/100/agent/get-fsinfo", agentBody(`[{"name":"sda1","mountpoint":"/","type":"ext4","used-bytes":100,"total-bytes":1000},{"name":"sda2","mountpoint":"/boot","type":"ext4","used-bytes":10,"total-bytes":200},{"name":"sdb1","mountpoint":"/data","type":"xfs","used-bytes":5,"total-bytes":500}]`))
	for _, call := range []string{"get-osinfo", "get-host-name", "network-get-interfaces", "get-fsinfo"} {
		f.set(prefix+"/qemu/102/agent/"+call, http.StatusInternalServerError, "QEMU guest agent is not running")
	}
	f.ok(prefix+"/lxc/200/interfaces", `{"data":[{"name":"lo","inet":"127.0.0.1/8"},{"name":"eth0","hwaddr":"aa:bb","inet":"10.0.0.9/24","inet6":"fd00::9/64"}]}`)
	return f
}

type guestsHarness struct {
	s    *guestsScraper
	logs *observer.ObservedLogs
}

func newGuests(t *testing.T, f *fakePVE, mutate func(*Config)) *guestsHarness {
	t.Helper()
	cfg := validConfig()
	cfg.BaseURLs = []string{f.url()}
	cfg.Guests = GuestsConfig{ScraperConfig: ScraperConfig{
		Enabled: true, TokenID: "otel@pve!guests", TokenSecret: "guests-secret",
		CollectionInterval: 15 * time.Minute, Timeout: time.Second,
	}, Node: "pve1"}
	if mutate != nil {
		mutate(cfg)
	}
	c, err := newAPIClient(cfg, cfg.Guests.TokenID, cfg.Guests.TokenSecret)
	require.NoError(t, err)
	core, logs := observer.New(zapcore.DebugLevel)
	return &guestsHarness{s: newGuestsScraper(zap.New(core), cfg, c, "pve1"), logs: logs}
}

// factsOf returns the change-event records as vmid -> fact -> value, in order.
func factRecords(logs plog.Logs) []map[string]string {
	var out []map[string]string
	rls := logs.ResourceLogs()
	for i := 0; i < rls.Len(); i++ {
		sls := rls.At(i).ScopeLogs()
		for j := 0; j < sls.Len(); j++ {
			recs := sls.At(j).LogRecords()
			for k := 0; k < recs.Len(); k++ {
				attrs := map[string]string{"body": recs.At(k).Body().AsString()}
				recs.At(k).Attributes().Range(func(key string, v pcommon.Value) bool {
					attrs[key] = v.AsString()
					return true
				})
				out = append(out, attrs)
			}
		}
	}
	return out
}

func sweep(t *testing.T, h *guestsHarness, now time.Time) ([]point, []map[string]string) {
	t.Helper()
	md, logs, err := h.s.sweep(context.Background(), now)
	require.NoError(t, err)
	h.s.commit() // the records were delivered
	return flatten(md), factRecords(logs)
}

func factsFor(recs []map[string]string, vmid string) map[string]string {
	out := map[string]string{}
	for _, r := range recs {
		if r["pve.vmid"] == vmid {
			out[r["pve.fact"]] = r["pve.fact.value"]
		}
	}
	return out
}

var t0 = time.Unix(1_000_000, 0)

func TestGuestWithThreeMountsGivesSixSeries(t *testing.T) {
	h := newGuests(t, guestsFake(t), nil)

	pts, _ := sweep(t, h, t0)

	usage := find(pts, "pve.guest.filesystem.usage")
	size := find(pts, "pve.guest.filesystem.size")
	require.Len(t, usage, 3)
	require.Len(t, size, 3)
	root := find(pts, "pve.guest.filesystem.usage", "mountpoint", "/")
	require.Len(t, root, 1)
	assert.Equal(t, "ext4", root[0].attrs["fstype"])
	assert.Equal(t, "sda1", root[0].attrs["device"])
	assert.InDelta(t, 100, root[0].value, 1e-9)
	assert.Equal(t, "By", root[0].unit)
	assert.InDelta(t, 1000, find(pts, "pve.guest.filesystem.size", "mountpoint", "/")[0].value, 1e-9)
	assert.Equal(t, "100", root[0].attrs["pve.vmid"])
}

func TestOnlyRunningGuestsGetAgentCalls(t *testing.T) {
	f := guestsFake(t)
	h := newGuests(t, f, nil)

	sweep(t, h, t0)

	for _, hit := range f.hitList() {
		assert.NotContains(t, hit, "/qemu/101/", "stopped guest")
		assert.NotContains(t, hit, "/lxc/201/", "stopped container")
	}
	assert.Equal(t, 1, f.countOf(prefix+"/qemu/100/agent/get-osinfo"))
}

func TestOnlyTheConfiguredNodeIsCalled(t *testing.T) {
	f := guestsFake(t)
	sweep(t, newGuests(t, f, nil), t0)

	for _, hit := range f.hitList() {
		assert.True(t, strings.HasPrefix(hit, prefix+"/"), hit)
	}
}

// A central poller runs one instance per node against one endpoint: the node in the path comes from
// guests.node, never from the address of the API that answers.
func TestTwoNodesThroughOneEndpointEachReadOnlyTheirOwnPath(t *testing.T) {
	f := newFakePVE(t, false)
	for _, node := range []string{"pve12", "pve13"} {
		f.ok("/api2/json/nodes/"+node+"/qemu", `{"data":[{"vmid":100,"name":"web","status":"running"}]}`)
		f.ok("/api2/json/nodes/"+node+"/lxc", `{"data":[]}`)
		for _, call := range []string{"get-osinfo", "get-host-name", "network-get-interfaces", "get-fsinfo"} {
			f.ok("/api2/json/nodes/"+node+"/qemu/100/agent/"+call, agentBody(`[]`))
		}
	}
	for _, node := range []string{"pve12", "pve13"} {
		h := newGuests(t, f, func(c *Config) { c.Guests.Node = node })
		h.s.node = node

		_, recs := sweep(t, h, t0)

		for _, r := range recs {
			assert.Equal(t, node, r["pve.node"])
		}
		assert.Equal(t, 1, f.countOf("/api2/json/nodes/"+node+"/qemu"))
	}
	for _, hit := range f.hitList() {
		assert.True(t, strings.HasPrefix(hit, "/api2/json/nodes/pve12/") || strings.HasPrefix(hit, "/api2/json/nodes/pve13/"), hit)
	}
}

func TestFirstSweepGivesOneRecordPerFactAndAChangedAddressGivesOneMore(t *testing.T) {
	f := guestsFake(t)
	h := newGuests(t, f, nil)

	_, first := sweep(t, h, t0)

	facts := factsFor(first, "100")
	assert.Equal(t, "Debian GNU/Linux 12 (bookworm)", facts["os"])
	assert.Equal(t, "web01", facts["hostname"])
	assert.Equal(t, "10.0.0.5", facts["addresses"], "loopback and link-local addresses are not facts")
	assert.Len(t, first, 3+1+1, "three facts for guest 100, the container addresses and the unavailable agent of 102")
	assert.Equal(t, "pve guest fact", first[0]["body"])
	assert.Equal(t, "pve1", first[0]["pve.node"])

	f.ok(prefix+"/qemu/100/agent/network-get-interfaces", agentBody(`[{"name":"eth0","ip-addresses":[{"ip-address":"10.0.0.6","ip-address-type":"ipv4","prefix":24}]}]`))
	_, second := sweep(t, h, t0.Add(time.Hour))

	assert.Len(t, second, 1)
	assert.Equal(t, "addresses", second[0]["pve.fact"])
	assert.Equal(t, "10.0.0.6", second[0]["pve.fact.value"])
}

func TestTwoSweepsWithUnchangedFactsGiveNoSecondRecord(t *testing.T) {
	h := newGuests(t, guestsFake(t), nil)

	sweep(t, h, t0)
	pts, second := sweep(t, h, t0.Add(time.Hour))

	assert.Empty(t, second)
	assert.NotEmpty(t, pts, "the filesystem metrics are sent every sweep")
}

func TestContainerGivesAddressesFromInterfacesAndNoAgentCall(t *testing.T) {
	f := guestsFake(t)
	h := newGuests(t, f, nil)

	pts, recs := sweep(t, h, t0)

	assert.Equal(t, "10.0.0.9,fd00::9", factsFor(recs, "200")["addresses"])
	assert.Len(t, factsFor(recs, "200"), 1)
	for _, hit := range f.hitList() {
		assert.False(t, strings.Contains(hit, "/lxc/200/agent"), hit)
	}
	assert.Empty(t, find(pts, "pve.guest.filesystem.usage", "pve.vmid", "200"))
}

func TestAgentNotRunningIsRecordedOnceWithoutAnErrorAndCalledAgainOnlyAtTheBackoff(t *testing.T) {
	f := guestsFake(t)
	h := newGuests(t, f, nil)

	_, first := sweep(t, h, t0)

	assert.Equal(t, "unavailable", factsFor(first, "102")["agent"])
	assert.Empty(t, h.logs.FilterLevelExact(zapcore.WarnLevel).All())
	assert.Empty(t, h.logs.FilterLevelExact(zapcore.ErrorLevel).All())
	calls := f.countOf(prefix + "/qemu/102/agent/get-osinfo")
	assert.Equal(t, 1, calls)

	_, early := sweep(t, h, t0.Add(time.Minute))
	assert.Equal(t, calls, f.countOf(prefix+"/qemu/102/agent/get-osinfo"), "still backing off")
	assert.Empty(t, factsFor(early, "102"))

	_, later := sweep(t, h, t0.Add(16*time.Minute))
	assert.Equal(t, calls+1, f.countOf(prefix+"/qemu/102/agent/get-osinfo"), "checked again at the backoff")
	assert.Empty(t, factsFor(later, "102"), "recorded once, not again")
}

func TestAgentThatComesBackIsRecorded(t *testing.T) {
	f := guestsFake(t)
	h := newGuests(t, f, nil)
	sweep(t, h, t0)
	for _, call := range []string{"get-osinfo", "get-host-name", "network-get-interfaces", "get-fsinfo"} {
		f.ok(prefix+"/qemu/102/agent/"+call, agentBody(`{"host-name":"back"}`))
	}
	f.ok(prefix+"/qemu/102/agent/network-get-interfaces", agentBody(`[]`))
	f.ok(prefix+"/qemu/102/agent/get-fsinfo", agentBody(`[]`))

	_, recs := sweep(t, h, t0.Add(16*time.Minute))

	assert.Equal(t, "available", factsFor(recs, "102")["agent"])
}

func TestBackoffGrowsPerGuestAndNeverPassesADay(t *testing.T) {
	base := 15 * time.Minute
	assert.Equal(t, 15*time.Minute, backoffFor(base, 1))
	assert.Equal(t, 30*time.Minute, backoffFor(base, 2))
	assert.Equal(t, time.Hour, backoffFor(base, 3))
	assert.Equal(t, 24*time.Hour, backoffFor(base, 9))
	assert.Equal(t, 24*time.Hour, backoffFor(base, 500), "no overflow at a large count")

	f := guestsFake(t)
	for _, call := range []string{"get-osinfo", "get-host-name", "network-get-interfaces", "get-fsinfo"} {
		f.set(prefix+"/qemu/102/agent/"+call, http.StatusInternalServerError, "internal error")
	}
	h := newGuests(t, f, nil)
	now := t0
	var gaps []time.Duration
	for i := 0; i < 12; i++ {
		sweep(t, h, now)
		st := h.s.state[102]
		gaps = append(gaps, st.nextTry.Sub(now))
		now = st.nextTry
	}
	for i := 1; i < len(gaps); i++ {
		assert.GreaterOrEqual(t, gaps[i], gaps[i-1], "growing")
		assert.LessOrEqual(t, gaps[i], 24*time.Hour)
	}
	assert.Equal(t, 24*time.Hour, gaps[len(gaps)-1])
}

func TestStuckGuestDoesNotDelayTheNextCallBeyondTheTimeout(t *testing.T) {
	f := guestsFake(t)
	f.delay = 400 * time.Millisecond
	f.delayOnly = "/agent/"
	h := newGuests(t, f, func(c *Config) { c.Guests.Timeout = 50 * time.Millisecond })

	start := time.Now()
	_, _, err := h.s.sweep(context.Background(), t0)

	require.NoError(t, err, "a timeout of one guest is not a sweep failure")
	assert.Less(t, time.Since(start), 2*time.Second)
	assert.Equal(t, 1, f.maxConcurrent(), "one request in flight")
}

func TestForbiddenAbortsTheSweepAndNamesThePrivilegeFromTheAnswer(t *testing.T) {
	for _, priv := range []string{"VM.Monitor", "VM.GuestAgent.Audit"} {
		f := guestsFake(t)
		f.set(prefix+"/qemu/100/agent/get-osinfo", http.StatusForbidden, "Permission check failed (/vms/100, "+priv+")")
		h := newGuests(t, f, nil)

		_, _, err := h.s.sweep(context.Background(), t0)

		var apiErr *apiError
		require.ErrorAs(t, err, &apiErr)
		assert.Equal(t, http.StatusForbidden, apiErr.Status)
		assert.Contains(t, privilegeMessage(apiErr), priv)
		assert.Zero(t, f.countOf(prefix+"/qemu/102/agent/get-osinfo"), "the sweep stopped")
	}
}

func TestForbiddenWithoutAPrivilegeInTheAnswerNamesBothPrivileges(t *testing.T) {
	msg := privilegeMessage(&apiError{Status: http.StatusForbidden, Message: "denied"})

	assert.Contains(t, msg, "VM.Monitor")
	assert.Contains(t, msg, "VM.GuestAgent.Audit")
	assert.Contains(t, msg, "VM.Audit")
}

func TestUnauthorizedAbortsTheSweep(t *testing.T) {
	f := guestsFake(t)
	f.set(prefix+"/qemu/100/agent/get-osinfo", http.StatusUnauthorized, "invalid token")
	h := newGuests(t, f, nil)

	_, _, err := h.s.sweep(context.Background(), t0)

	var apiErr *apiError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, http.StatusUnauthorized, apiErr.Status)
}

func TestGuestListFailureReturnsAnError(t *testing.T) {
	f := guestsFake(t)
	f.set(prefix+"/qemu", http.StatusInternalServerError, "boom")
	h := newGuests(t, f, nil)

	md, _, err := h.s.sweep(context.Background(), t0)

	require.Error(t, err)
	assert.Zero(t, md.MetricCount())
}

func TestStartOffsetsDifferBetweenScrapers(t *testing.T) {
	interval := 15 * time.Minute
	a := startOffset(interval, rand.New(rand.NewSource(1)))
	b := startOffset(interval, rand.New(rand.NewSource(2)))

	assert.NotEqual(t, a, b)
	assert.GreaterOrEqual(t, a, initialDelay)
	assert.LessOrEqual(t, a, initialDelay+interval/10)
}

func TestLocalNodeIsTheShortHostname(t *testing.T) {
	assert.Equal(t, "pve12", shortHostname("pve12.lab.example"))
	assert.Equal(t, "pve12", shortHostname("pve12"))
	assert.NotEmpty(t, localNode())
}

func TestLoopbackAndLinkLocalAddressesAreDropped(t *testing.T) {
	got := joinAddresses([]string{"127.0.0.1/8", "::1", "fe80::1/64", "169.254.1.2", "10.0.0.5/24", "fd00::9", "10.0.0.5"})

	assert.Equal(t, "10.0.0.5,fd00::9", got)
}

func TestNotCommittedFactsAreSentAgainAndCommittedOnesAreNot(t *testing.T) {
	h := newGuests(t, guestsFake(t), nil)

	_, lg, err := h.s.sweep(context.Background(), t0)
	require.NoError(t, err)
	first := factRecords(lg)
	h.s.discard() // the send failed

	_, lg, err = h.s.sweep(context.Background(), t0.Add(time.Hour))
	require.NoError(t, err)
	again := factRecords(lg)
	h.s.commit()

	_, lg, err = h.s.sweep(context.Background(), t0.Add(2*time.Hour))
	require.NoError(t, err)

	assert.NotEmpty(t, first)
	assert.Equal(t, len(first), len(again), "a lost send gives the same records again")
	assert.Empty(t, factRecords(lg), "after a delivered sweep nothing is repeated")
}

func TestFailedLogsSendKeepsTheChangeForTheNextSweep(t *testing.T) {
	f := guestsFake(t)
	cfg := bothEnabled(f)
	cfg.Resources.Enabled = false
	set, _ := settingsWithLogs()
	r, err := newReceiver(set, cfg)
	require.NoError(t, err)
	r.setMetrics(consumertest.NewNop())
	r.setLogs(consumertest.NewErr(errors.New("queue full")))

	r.sweepGuestsOnce(context.Background())
	sink := new(consumertest.LogsSink)
	r.setLogs(sink)
	r.sweepGuestsOnce(context.Background())
	r.sweepGuestsOnce(context.Background())

	assert.Equal(t, 5, sink.LogRecordCount(), "the second sweep delivers what the first lost; the third finds nothing new")
}

func TestUnwiredLogsPipelineLosesNoFacts(t *testing.T) {
	f := guestsFake(t)
	cfg := bothEnabled(f)
	cfg.Resources.Enabled = false
	set, _ := settingsWithLogs()
	r, err := newReceiver(set, cfg)
	require.NoError(t, err)
	r.setMetrics(consumertest.NewNop())

	r.sweepGuestsOnce(context.Background())
	sink := new(consumertest.LogsSink)
	r.setLogs(sink)
	r.sweepGuestsOnce(context.Background())

	assert.Equal(t, 5, sink.LogRecordCount())
}

func TestGuestThatEntersBackoffGivesOneWarnAndNotOneAnInterval(t *testing.T) {
	f := guestsFake(t)
	for _, call := range []string{"get-osinfo", "get-host-name", "network-get-interfaces", "get-fsinfo"} {
		f.set(prefix+"/qemu/102/agent/"+call, http.StatusInternalServerError, "internal error")
	}
	h := newGuests(t, f, nil)

	sweep(t, h, t0)
	warnings := h.logs.FilterLevelExact(zapcore.WarnLevel).All()
	require.Len(t, warnings, 1)
	assert.Equal(t, int64(102), warnings[0].ContextMap()["vmid"])

	sweep(t, h, h.s.state[102].nextTry)
	assert.Len(t, h.logs.FilterLevelExact(zapcore.WarnLevel).All(), 1, "the second failure in a row is quiet")
}

func TestPathsEscapeTheNodeName(t *testing.T) {
	f := newFakePVE(t, false)
	f.ok("/api2/json/nodes/a/b/qemu", `{"data":[]}`)
	f.ok("/api2/json/nodes/a/b/lxc", `{"data":[]}`)
	h := newGuests(t, f, func(c *Config) { c.Guests.Node = "a/b" })
	h.s.node = "a/b"

	_, _, err := h.s.sweep(context.Background(), t0)

	require.NoError(t, err)
	assert.Contains(t, f.escapedList(), "/api2/json/nodes/a%2Fb/qemu")
}
