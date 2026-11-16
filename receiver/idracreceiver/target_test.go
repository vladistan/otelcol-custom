// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package idracreceiver

import (
	"context"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

type rig struct {
	fake      *fakeRedfish
	clock     *fakeClock
	target    *target
	logs      *observer.ObservedLogs
	authFails []string
	skips     []string // "<target>/<part>" of every skipped read
}

// writeCA writes the certificate of the fake server as the CA file.
func writeCA(t *testing.T, f *fakeRedfish) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "ca.pem")
	require.NoError(t, os.WriteFile(p, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: f.srv.Certificate().Raw}), 0o600))
	return p
}

func newRig(t *testing.T, f *fakeRedfish, mutate func(*TargetConfig)) *rig {
	t.Helper()
	cfg := validTarget()
	cfg.Address = f.srv.URL
	cfg.CAFile = writeCA(t, f)
	if mutate != nil {
		mutate(&cfg)
	}
	core, logs := observer.New(zapcore.DebugLevel)
	clock := &fakeClock{t: time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)}
	r := &rig{fake: f, clock: clock, logs: logs}
	tg, err := newTarget(cfg.resolved(), &env{
		now: clock.Now, logger: zap.New(core), authFailed: func(name string) { r.authFails = append(r.authFails, name) },
		skipped: func(name, part string) { r.skips = append(r.skips, name+"/"+part) },
	})
	require.NoError(t, err)
	r.target = tg
	t.Cleanup(func() { tg.close(context.Background()) })
	return r
}

func (r *rig) scrape(t *testing.T) pmetric.Metrics {
	t.Helper()
	return r.target.scrape(t.Context())
}

func up(t *testing.T, md pmetric.Metrics) (int64, string) {
	t.Helper()
	dps := dataPoints(md, "hw.bmc.up")
	require.Equal(t, 1, dps.Len())
	reason, _ := dps.At(0).Attributes().Get("reason")
	return dps.At(0).IntValue(), reason.Str()
}

func dataPoints(md pmetric.Metrics, name string) pmetric.NumberDataPointSlice {
	if md.ResourceMetrics().Len() == 0 {
		return pmetric.NewNumberDataPointSlice()
	}
	ms := md.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics()
	for i := 0; i < ms.Len(); i++ {
		if ms.At(i).Name() == name {
			return ms.At(i).Gauge().DataPoints()
		}
	}
	return pmetric.NewNumberDataPointSlice()
}

func metricUnit(md pmetric.Metrics, name string) string {
	ms := md.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics()
	for i := 0; i < ms.Len(); i++ {
		if ms.At(i).Name() == name {
			return ms.At(i).Unit()
		}
	}
	return ""
}

func names(md pmetric.Metrics) []string {
	var out []string
	if md.ResourceMetrics().Len() == 0 {
		return out
	}
	ms := md.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics()
	for i := 0; i < ms.Len(); i++ {
		out = append(out, ms.At(i).Name())
	}
	return out
}

func attr(dp pmetric.NumberDataPoint, key string) string {
	v, _ := dp.Attributes().Get(key)
	return v.Str()
}

// byID finds the point whose hw.id contains the fragment.
func byID(t *testing.T, md pmetric.Metrics, metric, fragment string) pmetric.NumberDataPoint {
	t.Helper()
	dps := dataPoints(md, metric)
	for i := 0; i < dps.Len(); i++ {
		if id := attr(dps.At(i), "hw.id"); strings.Contains(id, fragment) {
			return dps.At(i)
		}
	}
	t.Fatalf("no %s point with an id containing %q", metric, fragment)
	return pmetric.NumberDataPoint{}
}

// Login and requests.

func TestOneSessionPostThenTokenOnEveryRequestAndNeverAnAuthorizationHeader(t *testing.T) {
	f := newFakeRedfish()
	defer f.close()
	r := newRig(t, f, nil)

	md := r.scrape(t)

	u, _ := up(t, md)
	assert.Equal(t, int64(1), u)
	assert.Equal(t, 1, f.logins)
	assert.False(t, f.authHeader, "the session token replaces the Authorization header")
	assert.Greater(t, f.count("GET", sys), 0)
}

func TestTheSessionIsReusedAcrossScrapes(t *testing.T) {
	f := newFakeRedfish()
	defer f.close()
	r := newRig(t, f, nil)

	r.scrape(t)
	r.scrape(t)

	assert.Equal(t, 1, f.logins)
}

func TestA401GivesOneNewLoginAndOneRetry(t *testing.T) {
	f := newFakeRedfish()
	defer f.close()
	r := newRig(t, f, nil)
	r.scrape(t)
	f.forceOnce(sys, 401)

	md := r.scrape(t)

	u, _ := up(t, md)
	assert.Equal(t, int64(1), u)
	assert.Equal(t, 2, f.logins, "one new login after the 401")
	assert.Equal(t, 1, pointCount(md, "hw.fan.speed"))
}

func TestASecond401GivesUpZeroWithReasonAuth(t *testing.T) {
	f := newFakeRedfish()
	defer f.close()
	r := newRig(t, f, nil)
	f.forceStatus(sys, 401)

	md := r.scrape(t)

	u, reason := up(t, md)
	assert.Equal(t, int64(0), u)
	assert.Equal(t, "auth", reason)
	assert.Equal(t, []string{"db-1"}, r.authFails)
}

func TestARefusedLoginIsAnAuthFailure(t *testing.T) {
	f := newFakeRedfish()
	defer f.close()
	f.loginStatus = 401
	r := newRig(t, f, nil)

	md := r.scrape(t)

	_, reason := up(t, md)
	assert.Equal(t, "auth", reason)
	assert.ElementsMatch(t, []string{"hw.bmc.up", "hw.bmc.scrape.duration"}, names(md))
}

func TestShutdownDeletesTheSession(t *testing.T) {
	f := newFakeRedfish()
	defer f.close()
	r := newRig(t, f, nil)
	r.scrape(t)

	r.target.close(context.Background())

	assert.Equal(t, 1, f.deletes)
	r.target.close(context.Background())
	assert.Equal(t, 1, f.deletes, "a second close sends nothing")
}

func TestA500OnAPathGivesUpZeroAndNoZeroValuedSeries(t *testing.T) {
	f := newFakeRedfish()
	defer f.close()
	r := newRig(t, f, nil)
	f.forceStatus(chassis+"/Thermal", 500)

	md := r.scrape(t)

	u, _ := up(t, md)
	assert.Equal(t, int64(0), u)
	assert.ElementsMatch(t, []string{"hw.bmc.up", "hw.bmc.scrape.duration"}, names(md))
}

func TestAStalledRequestEndsAtRequestTimeoutAndNeverOverlapsAnother(t *testing.T) {
	f := newFakeRedfish()
	defer f.close()
	f.stall[chassis+"/Thermal"] = 5 * time.Second
	r := newRig(t, f, func(c *TargetConfig) { c.RequestTimeout = 100 * time.Millisecond; c.ScrapeTimeout = 2 * time.Second })

	start := time.Now()
	md := r.scrape(t)

	assert.Less(t, time.Since(start), 2*time.Second)
	_, reason := up(t, md)
	assert.Equal(t, "timeout", reason)
	assert.Equal(t, 1, f.maxInFlight, "one request in flight")
}

func TestTheScrapeTimeoutCancelsTheWholeScrape(t *testing.T) {
	f := newFakeRedfish()
	defer f.close()
	for _, p := range []string{sys, chassis, chassis + "/Thermal"} {
		f.stall[p] = 150 * time.Millisecond
	}
	r := newRig(t, f, func(c *TargetConfig) { c.RequestTimeout = time.Second; c.ScrapeTimeout = 300 * time.Millisecond })

	start := time.Now()
	md := r.scrape(t)

	assert.Less(t, time.Since(start), 2*time.Second)
	_, reason := up(t, md)
	assert.Equal(t, "timeout", reason)
}

func TestAPathThatAnswers404IsMarkedAbsentWithOneInfoAndNeverAskedAgain(t *testing.T) {
	f := newFakeRedfish()
	defer f.close()
	f.remove(sys + "/Processors")
	r := newRig(t, f, nil)

	md := r.scrape(t)
	r.scrape(t)

	u, _ := up(t, md)
	assert.Equal(t, int64(1), u)
	assert.Equal(t, 1, f.count("GET", sys+"/Processors"), "asked once and not again")
	infos := r.logs.FilterLevelExact(zapcore.InfoLevel).FilterMessageSnippet("absent").FilterField(zap.String("path", sys+"/Processors"))
	assert.Equal(t, 1, infos.Len())
	assert.Equal(t, 1, pointCount(md, "hw.fan.speed"), "the other series are still there")
}

func TestPowerReadsTheLegacyResourceFirst(t *testing.T) {
	f := newFakeRedfish()
	defer f.close()
	r := newRig(t, f, nil)

	md := r.scrape(t)

	assert.Equal(t, 0, f.count("GET", chassis+"/PowerSubsystem"), "the subsystem is not read while the legacy Power answers")
	assert.Equal(t, 1, pointCount(md, "hw.host.power"))
	assert.Equal(t, 2, pointCount(md, "hw.power_supply.usage"))
}

func TestPowerSubsystemWhenTheBMCServesIt(t *testing.T) {
	f := newFakeRedfish()
	defer f.close()
	f.remove(chassis + "/Power")
	f.set(chassis+"/PowerSubsystem", obj{"PowerSupplies": ref(chassis + "/PowerSubsystem/PowerSupplies"), "Status": obj{"Health": "OK"}})
	f.set(chassis+"/PowerSubsystem/PowerSupplies", obj{"Members": []obj{
		{"Id": "PSU.Slot.1", "Name": "PS1", "PowerCapacityWatts": 800, "Status": obj{"Health": "Warning"}},
	}})
	r := newRig(t, f, nil)

	md := r.scrape(t)

	assert.Equal(t, 1, pointCount(md, "hw.power_supply.limit"))
	assert.InDelta(t, 800, byID(t, md, "hw.power_supply.limit", "PSU.Slot.1").DoubleValue(), 1e-9)
	assert.Equal(t, 1, f.count("GET", chassis+"/Power"), "the legacy Power is asked first and answered 404")
}

// TLS.

func TestAnUntrustedCertificateGivesUpZeroReasonTLSAndNoInsecureRetry(t *testing.T) {
	f := newFakeRedfish()
	defer f.close()
	r := newRig(t, f, func(c *TargetConfig) { c.CAFile = "" })

	md := r.scrape(t)

	u, reason := up(t, md)
	assert.Equal(t, int64(0), u)
	assert.Equal(t, "tls", reason)
	assert.Equal(t, 0, f.logins, "no request got past the handshake, and none was retried without verification")
}

func TestACAFileTheServerCertificateIsSignedByConnects(t *testing.T) {
	f := newFakeRedfish()
	defer f.close()
	r := newRig(t, f, nil)

	u, _ := up(t, r.scrape(t))

	assert.Equal(t, int64(1), u)
}

func TestInsecureSkipVerifyIsAnExplicitOptIn(t *testing.T) {
	f := newFakeRedfish()
	defer f.close()
	r := newRig(t, f, func(c *TargetConfig) { c.CAFile = ""; c.InsecureSkipVerify = true })

	u, _ := up(t, r.scrape(t))

	assert.Equal(t, int64(1), u)
}

func TestABadCAFileFailsAtCreation(t *testing.T) {
	cfg := validTarget()
	cfg.CAFile = filepath.Join(t.TempDir(), "absent.pem")
	_, err := newTarget(cfg.resolved(), &env{now: time.Now, logger: zap.NewNop(), authFailed: func(string) {}})
	require.Error(t, err)

	bad := filepath.Join(t.TempDir(), "bad.pem")
	require.NoError(t, os.WriteFile(bad, []byte("not a certificate"), 0o600))
	cfg.CAFile = bad
	_, err = newTarget(cfg.resolved(), &env{now: time.Now, logger: zap.NewNop(), authFailed: func(string) {}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no certificate")
}

// Back-off.

func TestAuthFailureBacksOff15Then30MinutesAndStopsAfterTheThirdUntilRestart(t *testing.T) {
	f := newFakeRedfish()
	defer f.close()
	f.loginStatus = 401
	r := newRig(t, f, nil)

	r.scrape(t)
	assert.Equal(t, 1, f.logins)
	r.clock.Advance(14 * time.Minute)
	_, reason := up(t, r.scrape(t))
	assert.Equal(t, "backoff", reason)
	assert.Equal(t, 1, f.logins)

	r.clock.Advance(time.Minute)
	r.scrape(t)
	assert.Equal(t, 2, f.logins)
	r.clock.Advance(30 * time.Minute)
	r.scrape(t)
	assert.Equal(t, 3, f.logins)

	r.clock.Advance(48 * time.Hour)
	_, reason = up(t, r.scrape(t))
	assert.Equal(t, "backoff", reason)
	assert.Equal(t, 3, f.logins, "stopped until restart")
	assert.Len(t, r.authFails, 3)
}

func TestAuthBackoffDoublesToACeilingOfSixHours(t *testing.T) {
	assert.Equal(t, 15*time.Minute, authBackoff(1))
	assert.Equal(t, 30*time.Minute, authBackoff(2))
	assert.Equal(t, 6*time.Hour, authBackoff(9))
}

// Metrics.

func pointCount(md pmetric.Metrics, name string) int { return dataPoints(md, name).Len() }

func TestResourceAttributes(t *testing.T) {
	f := newFakeRedfish()
	defer f.close()
	r := newRig(t, f, nil)

	attrs := r.scrape(t).ResourceMetrics().At(0).Resource().Attributes()

	got := map[string]string{}
	attrs.Range(func(k string, v pcommon.Value) bool { got[k] = v.Str(); return true })
	assert.Equal(t, "db-1", got["host.name"])
	assert.Equal(t, "dell", got["hw.vendor"])
	assert.Equal(t, "redfish", got["hw.bmc.protocol"])
	assert.Equal(t, "PowerEdge R640", got["hw.model"])
	assert.Equal(t, "ABC1234", got["hw.serial_number"])
	assert.Equal(t, "7.00.00.173", got["hw.firmware_version"])
	assert.Contains(t, got["server.address"], "127.0.0.1")
	assert.Len(t, got, 7)
}

func TestTemperaturesAndTheirUpperThresholds(t *testing.T) {
	f := newFakeRedfish()
	defer f.close()
	r := newRig(t, f, nil)

	md := r.scrape(t)

	assert.Equal(t, "Cel", metricUnit(md, "hw.temperature"))
	assert.Equal(t, 2, pointCount(md, "hw.temperature"))
	assert.InDelta(t, 22, byID(t, md, "hw.temperature", "SystemBoardInletTemp").DoubleValue(), 1e-9)
	assert.Equal(t, 3, pointCount(md, "hw.temperature.limit"), "critical and non-critical of the inlet, critical of the CPU")
	limits := map[string]float64{}
	dps := dataPoints(md, "hw.temperature.limit")
	for i := 0; i < dps.Len(); i++ {
		if strings.Contains(attr(dps.At(i), "hw.id"), "Inlet") {
			limits[attr(dps.At(i), "hw.limit_type")] = dps.At(i).DoubleValue()
		}
	}
	assert.Equal(t, map[string]float64{"critical": 47, "high.degraded": 42}, limits)
}

func TestFansInRPMAndInPercent(t *testing.T) {
	f := newFakeRedfish()
	defer f.close()
	r := newRig(t, f, nil)

	md := r.scrape(t)

	assert.InDelta(t, 6240, byID(t, md, "hw.fan.speed", "0").DoubleValue(), 1e-9)
	assert.Equal(t, "rpm", metricUnit(md, "hw.fan.speed"))
	assert.InDelta(t, 0.4, byID(t, md, "hw.fan.speed_ratio", "1").DoubleValue(), 1e-9)
	assert.Equal(t, 1, pointCount(md, "hw.fan.speed"))
	assert.Equal(t, 1, pointCount(md, "hw.fan.speed_ratio"))
}

func TestVoltageAndHostPower(t *testing.T) {
	f := newFakeRedfish()
	defer f.close()
	r := newRig(t, f, nil)

	md := r.scrape(t)

	assert.InDelta(t, 12.1, dataPoints(md, "hw.voltage").At(0).DoubleValue(), 1e-9)
	assert.Equal(t, "V", metricUnit(md, "hw.voltage"))
	assert.InDelta(t, 250, dataPoints(md, "hw.host.power").At(0).DoubleValue(), 1e-9)
	assert.Equal(t, "W", metricUnit(md, "hw.host.power"))
}

func TestTwoPowerSuppliesGiveTheThreeMetricsEach(t *testing.T) {
	f := newFakeRedfish()
	defer f.close()
	r := newRig(t, f, nil)

	md := r.scrape(t)

	assert.Equal(t, 2, pointCount(md, "hw.power_supply.usage"))
	assert.Equal(t, 2, pointCount(md, "hw.power_supply.limit"))
	assert.Equal(t, 2, pointCount(md, "hw.power_supply.utilization"))
	assert.InDelta(t, 120.0/750.0, byID(t, md, "hw.power_supply.utilization", "PSU.Slot.1").DoubleValue(), 1e-9)
}

func TestDriveSizeAndEndurance(t *testing.T) {
	f := newFakeRedfish()
	defer f.close()
	r := newRig(t, f, nil)

	md := r.scrape(t)

	assert.InDelta(t, 480103981056, dataPoints(md, "hw.physical_disk.size").At(0).DoubleValue(), 1)
	end := dataPoints(md, "hw.physical_disk.endurance_utilization").At(0)
	assert.InDelta(t, 0.2, end.DoubleValue(), 1e-9)
	assert.Equal(t, "used", attr(end, "hw.physical_disk.state"))
}

func TestDIMMSizeInBytesAndAnAbsentDIMMGivesNothing(t *testing.T) {
	f := newFakeRedfish()
	defer f.close()
	r := newRig(t, f, nil)

	md := r.scrape(t)

	require.Equal(t, 1, pointCount(md, "hw.memory.size"))
	assert.InDelta(t, 16384*1048576, dataPoints(md, "hw.memory.size").At(0).DoubleValue(), 1)
	assert.Equal(t, "By", metricUnit(md, "hw.memory.size"))
}

func TestStatusForEachOfTheEightTypesWithOnePointPerState(t *testing.T) {
	f := newFakeRedfish()
	defer f.close()
	r := newRig(t, f, nil)

	md := r.scrape(t)

	current := map[string]map[string]string{} // type -> name -> current state
	dps := dataPoints(md, "hw.status")
	perComponent := map[string]int{}
	for i := 0; i < dps.Len(); i++ {
		typ, name := attr(dps.At(i), "hw.type"), attr(dps.At(i), "hw.name")
		perComponent[typ+"/"+name]++
		if dps.At(i).IntValue() == 1 {
			if current[typ] == nil {
				current[typ] = map[string]string{}
			}
			current[typ][name] = attr(dps.At(i), "hw.state")
		}
	}
	for _, typ := range []string{"host", "enclosure", "fan", "power_supply", "physical_disk", "memory", "cpu", "bmc"} {
		assert.NotEmpty(t, current[typ], typ)
	}
	for key, n := range perComponent {
		assert.Equal(t, 5, n, key)
	}
	assert.Equal(t, "degraded", current["fan"]["System Board Fan2A"])
	assert.Equal(t, "failed", current["power_supply"]["PS2 Status"])
	assert.Equal(t, "degraded", current["cpu"]["CPU 2"])
}

func TestResourcesSelectsWhatIsRead(t *testing.T) {
	f := newFakeRedfish()
	defer f.close()
	r := newRig(t, f, func(c *TargetConfig) { c.Resources = []string{"system", "bmc"} })

	md := r.scrape(t)

	assert.Equal(t, 0, pointCount(md, "hw.temperature"))
	assert.Equal(t, 0, f.count("GET", chassis+"/Thermal"))
	assert.Equal(t, 0, f.count("GET", sys+"/Memory"))
	assert.Greater(t, pointCount(md, "hw.status"), 0)
}

func TestCollectionMembersAreFetchedOneByOneWhenTheBMCIgnoresExpand(t *testing.T) {
	f := newFakeRedfish()
	defer f.close()
	f.set(sys+"/Memory", obj{"Members": []obj{ref(sys + "/Memory/DIMM.Socket.A1")}})
	f.set(sys+"/Memory/DIMM.Socket.A1", obj{"Id": "DIMM.Socket.A1", "Name": "DIMM A1", "CapacityMiB": 8192, "Status": obj{"Health": "OK", "State": "Enabled"}})
	r := newRig(t, f, nil)

	md := r.scrape(t)

	assert.InDelta(t, 8192*1048576, dataPoints(md, "hw.memory.size").At(0).DoubleValue(), 1)
	assert.Equal(t, 1, f.count("GET", sys+"/Memory/DIMM.Socket.A1"))
}

func TestAStatusHealthOfTheBMCThatIsUnknownGivesNoStatusPoint(t *testing.T) {
	f := newFakeRedfish()
	defer f.close()
	f.set(mgr, obj{"FirmwareVersion": "7.00", "Status": obj{"Health": nil}})
	r := newRig(t, f, nil)

	md := r.scrape(t)

	dps := dataPoints(md, "hw.status")
	for i := 0; i < dps.Len(); i++ {
		assert.NotEqual(t, "bmc", attr(dps.At(i), "hw.type"))
	}
}

func TestDurationIsTheClockDifference(t *testing.T) {
	f := newFakeRedfish()
	defer f.close()
	r := newRig(t, f, nil)

	md := r.scrape(t)

	dur := dataPoints(md, "hw.bmc.scrape.duration")
	require.Equal(t, 1, dur.Len())
	assert.GreaterOrEqual(t, dur.At(0).DoubleValue(), 0.0)
}

// Secrets.

func TestNeitherThePasswordNorTheTokenReachesALogLine(t *testing.T) {
	cases := map[string]func(*fakeRedfish){
		"good scrape":   func(*fakeRedfish) {},
		"401":           func(f *fakeRedfish) { f.forceStatus(sys, 401) },
		"refused login": func(f *fakeRedfish) { f.loginStatus = 401 },
		"500":           func(f *fakeRedfish) { f.forceStatus(sys, 500) },
	}
	for name, arrange := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFakeRedfish()
			defer f.close()
			arrange(f)
			r := newRig(t, f, nil)

			r.scrape(t)
			tlsRig := newRig(t, f, func(c *TargetConfig) { c.CAFile = "" })
			tlsRig.scrape(t)

			for _, l := range []*observer.ObservedLogs{r.logs, tlsRig.logs} {
				require.NotZero(t, l.Len()+1)
				for _, e := range l.All() {
					assert.NotContains(t, e.Message, "secret")
					assert.NotContains(t, e.Message, sessionToken)
					for _, fld := range e.Context {
						assert.NotContains(t, fmt.Sprint(fld.String, fld.Interface), "secret")
						assert.NotContains(t, fmt.Sprint(fld.String, fld.Interface), sessionToken)
					}
				}
			}
		})
	}
}
