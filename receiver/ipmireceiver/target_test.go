// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package ipmireceiver

import (
	"context"
	"errors"
	"fmt"
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

// rig is one target against one fake board, with a fake clock and captured logs.
type rig struct {
	clock     *fakeClock
	board     *fakeBMC
	target    *target
	logs      *observer.ObservedLogs
	authFails []string
}

func newRig(t *testing.T, board *fakeBMC, clock *fakeClock, mutate func(*BMCConfig)) *rig {
	t.Helper()
	cfg := validBMC()
	if mutate != nil {
		mutate(&cfg)
	}
	core, logs := observer.New(zapcore.DebugLevel)
	r := &rig{clock: clock, board: board, logs: logs}
	r.target = newTarget(cfg.resolved(), &env{
		now:           clock.Now,
		dial:          board.dial,
		logger:        zap.New(core),
		scrapeTimeout: 2 * time.Second,
		authFailed:    func(name string) { r.authFails = append(r.authFails, name) },
	})
	return r
}

func (r *rig) scrape(t *testing.T) pmetric.Metrics {
	t.Helper()
	return r.target.scrape(t.Context())
}

func upValue(t *testing.T, md pmetric.Metrics) (int64, string) {
	t.Helper()
	dps := dataPoints(md, "hw.bmc.up")
	require.Equal(t, 1, dps.Len())
	reason, _ := dps.At(0).Attributes().Get("reason")
	return dps.At(0).IntValue(), reason.Str()
}

func TestGoodScrapeIsUpWithNoReasonAndTheClockDifferenceAsDuration(t *testing.T) {
	clock := newClock()
	board := labBoard(clock, 2, 1, 1)
	board.dialLatency = 1500 * time.Millisecond
	r := newRig(t, board, clock, nil)

	md := r.scrape(t)

	up, reason := upValue(t, md)
	assert.Equal(t, int64(1), up)
	assert.Empty(t, reason)
	dur := dataPoints(md, "hw.bmc.scrape.duration")
	require.Equal(t, 1, dur.Len())
	assert.InDelta(t, 1.5, dur.At(0).DoubleValue(), 1e-9)
}

func TestResourceAttributesComeFromTheTargetAndTheDeviceID(t *testing.T) {
	clock := newClock()
	r := newRig(t, labBoard(clock, 1, 0, 0), clock, func(c *BMCConfig) { c.Address = "192.0.2.12:623" })

	attrs := r.scrape(t).ResourceMetrics().At(0).Resource().Attributes()

	got := map[string]string{}
	attrs.Range(func(k string, v pcommon.Value) bool { got[k] = v.Str(); return true })
	assert.Equal(t, map[string]string{
		"host.name": "pve12", "hw.vendor": "supermicro", "server.address": "192.0.2.12", "hw.bmc.protocol": "ipmi",
		"hw.firmware_version": "3.26",
	}, got)
}

func TestLabBoardGives21Temperatures6FansAnd13Voltages(t *testing.T) {
	clock := newClock()
	r := newRig(t, labBoard(clock, 21, 6, 13), clock, nil)

	md := r.scrape(t)

	assert.Equal(t, 21, pointCount(md, "hw.temperature"))
	assert.Equal(t, 6, pointCount(md, "hw.fan.speed"))
	assert.Equal(t, 13, pointCount(md, "hw.voltage"))
}

func TestSensorValuesUnitsAndIDs(t *testing.T) {
	clock := newClock()
	board := labBoard(clock, 1, 1, 1)
	board.readings[1] = sensorReading{value: 24}
	board.readings[2] = sensorReading{value: 4950}
	board.readings[3] = sensorReading{value: 12.031}
	r := newRig(t, board, clock, nil)

	md := r.scrape(t)

	temp := dataPoints(md, "hw.temperature").At(0)
	assert.InDelta(t, 24, temp.DoubleValue(), 1e-9)
	id, _ := temp.Attributes().Get("hw.id")
	name, _ := temp.Attributes().Get("hw.name")
	assert.Equal(t, "temperature/1", id.Str())
	assert.Equal(t, "Temp 0", name.Str())
	assert.InDelta(t, 4950, dataPoints(md, "hw.fan.speed").At(0).DoubleValue(), 1e-9)
	assert.InDelta(t, 12.031, dataPoints(md, "hw.voltage").At(0).DoubleValue(), 1e-9)
}

func TestUpperCriticalThresholdGivesOneLimitPoint(t *testing.T) {
	clock := newClock()
	board := newBoard(clock, sdrRecord{number: 1, name: "CPU1 Temp", kind: kindTemperature, upperCritical: ptrTo(95)})
	board.readings[1] = sensorReading{value: 40}
	r := newRig(t, board, clock, nil)

	md := r.scrape(t)

	lim := dataPoints(md, "hw.temperature.limit")
	require.Equal(t, 1, lim.Len())
	assert.InDelta(t, 95, lim.At(0).DoubleValue(), 1e-9)
	lt, _ := lim.At(0).Attributes().Get("hw.limit_type")
	assert.Equal(t, "critical", lt.Str())
}

func TestSensorsWithoutAReadingOrOfAnUnmappedTypeGiveNoPointAndNoError(t *testing.T) {
	clock := newClock()
	board := newBoard(clock,
		sdrRecord{number: 1, name: "System Temp", kind: kindTemperature},
		sdrRecord{number: 2, name: "FAN 4", kind: kindFan},
		sdrRecord{number: 3, name: "CPU Temp", kind: kindOther},
		sdrRecord{number: 4, name: "DIMM A1", kind: kindTemperature},
	)
	board.readings[1] = sensorReading{value: 24}
	board.absent[2] = true
	board.absent[4] = true
	r := newRig(t, board, clock, nil)

	md := r.scrape(t)

	up, _ := upValue(t, md)
	assert.Equal(t, int64(1), up)
	assert.Equal(t, 1, pointCount(md, "hw.temperature"))
	assert.Equal(t, 0, pointCount(md, "hw.fan.speed"))
	assert.Zero(t, r.logs.FilterLevelExact(zapcore.WarnLevel).Len())
	assert.Zero(t, r.logs.FilterLevelExact(zapcore.ErrorLevel).Len())
}

func TestPowerSupplyStatusSensorsGiveStatusPointsOfTypePowerSupply(t *testing.T) {
	clock := newClock()
	board := newBoard(clock,
		sdrRecord{number: 1, name: "PS1 Status", kind: kindPowerSupply},
		sdrRecord{number: 2, name: "PS2 Status", kind: kindPowerSupply},
		sdrRecord{number: 3, name: "PS3 Status", kind: kindPowerSupply},
	)
	board.readings[1] = sensorReading{active: []uint8{0}}    // presence detected
	board.readings[2] = sensorReading{active: []uint8{0, 1}} // failure detected
	board.readings[3] = sensorReading{active: []uint8{0, 2}} // predictive failure
	r := newRig(t, board, clock, nil)

	md := r.scrape(t)

	states := statusByName(md)
	assert.Equal(t, "ok", states["PS1 Status"])
	assert.Equal(t, "failed", states["PS2 Status"])
	assert.Equal(t, "predicted_failure", states["PS3 Status"])
	assert.Equal(t, 15, pointCount(md, "hw.status"))
	typ, _ := dataPoints(md, "hw.status").At(0).Attributes().Get("hw.type")
	assert.Equal(t, "power_supply", typ.Str())
}

func TestAbsentPowerSupplyGivesNoStatus(t *testing.T) {
	clock := newClock()
	board := newBoard(clock, sdrRecord{number: 1, name: "PS2 Status", kind: kindPowerSupply})
	board.readings[1] = sensorReading{} // nothing asserted: not present
	r := newRig(t, board, clock, nil)

	assert.Equal(t, 0, pointCount(r.scrape(t), "hw.status"))
}

func TestChassisIntrusionOffsetZeroGivesFailedAndNoOffsetGivesOK(t *testing.T) {
	clock := newClock()
	board := newBoard(clock, sdrRecord{number: 1, name: "Chassis Intru", kind: kindIntrusion})
	r := newRig(t, board, clock, nil)

	board.readings[1] = sensorReading{active: []uint8{0}}
	md := r.scrape(t)
	assert.Equal(t, "failed", statusByName(md)["chassis intrusion"])
	typ, _ := dataPoints(md, "hw.status").At(0).Attributes().Get("hw.type")
	assert.Equal(t, "enclosure", typ.Str())

	board.readings[1] = sensorReading{}
	assert.Equal(t, "ok", statusByName(r.scrape(t))["chassis intrusion"])
}

// statusByName returns the current state of each hw.status component.
func statusByName(md pmetric.Metrics) map[string]string {
	out := map[string]string{}
	dps := dataPoints(md, "hw.status")
	for i := 0; i < dps.Len(); i++ {
		if dps.At(i).IntValue() != 1 {
			continue
		}
		name, _ := dps.At(i).Attributes().Get("hw.name")
		state, _ := dps.At(i).Attributes().Get("hw.state")
		out[name.Str()] = state.Str()
	}
	return out
}

func TestSDRIsReadOnceAndAgainOnAChangedStampOrAfterSixHours(t *testing.T) {
	clock := newClock()
	board := labBoard(clock, 2, 1, 1)
	r := newRig(t, board, clock, nil)

	r.scrape(t)
	r.scrape(t)
	assert.Equal(t, 1, board.sdrReads)

	board.stamp = board.stamp.Add(time.Hour)
	r.scrape(t)
	assert.Equal(t, 2, board.sdrReads)

	clock.Advance(6*time.Hour + time.Minute)
	r.scrape(t)
	assert.Equal(t, 3, board.sdrReads)
}

func TestEachScrapeReadsTheSelectedSensorsOnceAndNeverReadsTheWholeRepositoryAgain(t *testing.T) {
	clock := newClock()
	board := labBoard(clock, 2, 1, 1)
	r := newRig(t, board, clock, nil)

	r.scrape(t)
	r.scrape(t)

	assert.Equal(t, []uint8{1, 2, 3, 4, 1, 2, 3, 4}, board.sensorReads)
	require.NotEmpty(t, board.calls, "the session was used")
	for _, call := range board.calls {
		assert.Contains(t, []string{"GetDeviceID", "GetSDRsMap", "GetSensorReading", "GetDCMIPowerReading", "ListFRUDevices", "ReadFRU"}, call)
	}
}

func TestOneSessionIsOpenedAndClosedPerScrape(t *testing.T) {
	clock := newClock()
	board := labBoard(clock, 1, 1, 1)
	r := newRig(t, board, clock, nil)

	for i := 1; i <= 3; i++ {
		r.scrape(t)
		assert.Equal(t, i, board.opened)
		assert.Equal(t, i, board.closed)
	}
	assert.Equal(t, []string{"user", "user", "user"}, board.dials)
}

func TestSessionUsesTheConfiguredPrivilegeLevel(t *testing.T) {
	clock := newClock()
	board := labBoard(clock, 1, 0, 0)
	r := newRig(t, board, clock, func(c *BMCConfig) { c.PrivilegeLevel = "operator" })

	r.scrape(t)

	assert.Equal(t, []string{"operator"}, board.dials)
}

func TestFailedScrapeGivesOnlyUpZeroAndDurationAndNothingOfTheEarlierScrape(t *testing.T) {
	tests := map[string]struct {
		err    error
		reason string
	}{
		"timeout": {errTimeout, "timeout"},
		"auth":    {errAuth, "auth"},
		"refused": {errRefused, "refused"},
		"other":   {errors.New("boom"), "refused"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			clock := newClock()
			board := labBoard(clock, 2, 1, 1)
			r := newRig(t, board, clock, nil)
			good := r.scrape(t)
			require.Equal(t, 2, pointCount(good, "hw.temperature"))

			board.dialErr = tt.err
			md := r.scrape(t)

			up, reason := upValue(t, md)
			assert.Equal(t, int64(0), up)
			assert.Equal(t, tt.reason, reason)
			assert.ElementsMatch(t, []string{"hw.bmc.up", "hw.bmc.scrape.duration"}, metricNamesOf(md))
		})
	}
}

func TestAuthFailureBacksOff15Then30MinutesAndStopsAfterTheThirdUntilRestart(t *testing.T) {
	clock := newClock()
	board := labBoard(clock, 1, 0, 0)
	board.dialErr = errAuth
	r := newRig(t, board, clock, nil)

	// First failure: one attempt, then silence for 15 minutes.
	md := r.scrape(t)
	assert.Equal(t, 1, len(board.dials))
	up, reason := upValue(t, md)
	assert.Equal(t, int64(0), up)
	assert.Equal(t, "auth", reason)

	clock.Advance(14 * time.Minute)
	md = r.scrape(t)
	assert.Equal(t, 1, len(board.dials), "no attempt inside the 15 minutes")
	_, reason = upValue(t, md)
	assert.Equal(t, "backoff", reason)

	clock.Advance(time.Minute)
	r.scrape(t)
	assert.Equal(t, 2, len(board.dials), "second attempt at 15 minutes")

	clock.Advance(29 * time.Minute)
	r.scrape(t)
	assert.Equal(t, 2, len(board.dials), "no attempt inside the next 30 minutes")

	clock.Advance(time.Minute)
	r.scrape(t)
	assert.Equal(t, 3, len(board.dials), "third attempt at 30 minutes")

	clock.Advance(48 * time.Hour)
	md = r.scrape(t)
	assert.Equal(t, 3, len(board.dials), "stopped after the third failure until restart")
	_, reason = upValue(t, md)
	assert.Equal(t, "backoff", reason)
}

func TestAuthBackoffDoublesFromFifteenMinutesAndStopsAtSixHours(t *testing.T) {
	assert.Equal(t, 15*time.Minute, authBackoff(1))
	assert.Equal(t, 30*time.Minute, authBackoff(2))
	assert.Equal(t, time.Hour, authBackoff(3))
	assert.Equal(t, 2*time.Hour, authBackoff(4))
	assert.Equal(t, 4*time.Hour, authBackoff(5))
	assert.Equal(t, 6*time.Hour, authBackoff(6))
	assert.Equal(t, 6*time.Hour, authBackoff(20))
}

func TestAGoodScrapeResetsTheAuthFailuresInARow(t *testing.T) {
	clock := newClock()
	board := labBoard(clock, 1, 0, 0)
	r := newRig(t, board, clock, nil)

	board.dialErr = errAuth
	r.scrape(t)
	clock.Advance(15 * time.Minute)
	r.scrape(t)
	board.dialErr = nil
	clock.Advance(30 * time.Minute)
	up, _ := upValue(t, r.scrape(t))
	assert.Equal(t, int64(1), up)

	board.dialErr = errAuth
	r.scrape(t)
	clock.Advance(15 * time.Minute)
	r.scrape(t)
	assert.Equal(t, 5, len(board.dials), "two failures after the reset do not stop the BMC")
}

func TestAuthFailuresAreCountedPerBMC(t *testing.T) {
	clock := newClock()
	board := labBoard(clock, 1, 0, 0)
	board.dialErr = errAuth
	r := newRig(t, board, clock, nil)

	r.scrape(t)
	clock.Advance(15 * time.Minute)
	r.scrape(t)

	assert.Equal(t, []string{"pve12", "pve12"}, r.authFails)
}

func TestTimeoutAndRefusedDoNotBackOffBeyondTheNextScrape(t *testing.T) {
	clock := newClock()
	board := labBoard(clock, 1, 0, 0)
	board.dialErr = errRefused
	r := newRig(t, board, clock, nil)

	r.scrape(t)
	r.scrape(t)

	assert.Equal(t, 2, len(board.dials))
	assert.Empty(t, r.authFails)
}

func TestAHangingBMCGivesTimeoutAtTheDeadlineWhileAnotherIsScrapedOnTime(t *testing.T) {
	clock := newClock()
	hung := labBoard(clock, 1, 0, 0)
	hung.hang = true
	good := labBoard(clock, 1, 0, 0)
	rHung := newRig(t, hung, clock, nil)
	rHung.target.env.scrapeTimeout = 50 * time.Millisecond
	rGood := newRig(t, good, clock, nil)

	start := time.Now()
	mdHung := rHung.scrape(t)
	waited := time.Since(start)
	mdGood := rGood.scrape(t)

	_, reason := upValue(t, mdHung)
	assert.Equal(t, "timeout", reason)
	assert.GreaterOrEqual(t, waited, 50*time.Millisecond)
	up, _ := upValue(t, mdGood)
	assert.Equal(t, int64(1), up)
}

func TestSessionIsClosedWhenTheDeadlineEndsAReadInTheMiddle(t *testing.T) {
	clock := newClock()
	board := labBoard(clock, 1, 0, 0)
	r := newRig(t, board, clock, nil)
	r.target.env.dial = func(ctx context.Context, cfg BMCConfig, level string) (bmcSession, error) {
		s, err := board.dial(ctx, cfg, level)
		if err != nil {
			return nil, err
		}
		return &stallingSession{bmcSession: s}, nil
	}
	r.target.env.scrapeTimeout = 50 * time.Millisecond

	md := r.scrape(t)

	_, reason := upValue(t, md)
	assert.Equal(t, "timeout", reason)
	assert.Equal(t, 1, board.closed)
}

type stallingSession struct{ bmcSession }

func (s *stallingSession) GetDeviceID(ctx context.Context) (deviceInfo, error) {
	<-ctx.Done()
	return deviceInfo{}, ctx.Err()
}

// DCMI power.

func dcmiRig(t *testing.T) (*rig, *fakeBMC, *fakeClock) {
	t.Helper()
	clock := newClock()
	board := labBoard(clock, 1, 0, 0)
	r := newRig(t, board, clock, func(c *BMCConfig) { c.DCMIPower = true })
	return r, board, clock
}

func TestDCMIPowerIsOffByDefault(t *testing.T) {
	clock := newClock()
	board := labBoard(clock, 1, 0, 0)
	r := newRig(t, board, clock, nil)

	md := r.scrape(t)

	assert.Equal(t, 0, board.powerReads)
	assert.NotContains(t, board.dials, "administrator")
	assert.Equal(t, 0, pointCount(md, "hw.host.power"))
}

func TestDCMIPowerOpensOneAdministratorSessionPer5MinutesAndClosesItAtOnce(t *testing.T) {
	r, board, clock := dcmiRig(t)

	md := r.scrape(t)
	assert.Equal(t, []string{"user", "administrator"}, board.dials)
	assert.Equal(t, 1, board.powerReads)
	assert.Equal(t, board.opened, board.closed)
	assert.InDelta(t, 194, dataPoints(md, "hw.host.power").At(0).DoubleValue(), 1e-9)

	clock.Advance(time.Minute)
	md = r.scrape(t)
	assert.Equal(t, 1, board.powerReads, "no power read inside the 5 minutes")
	assert.Equal(t, 0, pointCount(md, "hw.host.power"))

	clock.Advance(4 * time.Minute)
	md = r.scrape(t)
	assert.Equal(t, 2, board.powerReads)
	assert.Equal(t, 1, pointCount(md, "hw.host.power"))
	assert.Equal(t, board.opened, board.closed)
}

func TestInvalidCommandMarksDCMIAbsentForGood(t *testing.T) {
	r, board, clock := dcmiRig(t)
	board.powerErrs = []error{completionError{code: completionInvalidCommand}}

	md := r.scrape(t)
	assert.Equal(t, 0, pointCount(md, "hw.host.power"))
	assert.Equal(t, 1, pointCount(md, "hw.temperature"))
	up, _ := upValue(t, md)
	assert.Equal(t, int64(1), up)

	clock.Advance(time.Hour)
	r.scrape(t)
	assert.Equal(t, 1, board.powerReads)
}

func TestInitializingAnswerIsRetriedAtTheNextInterval(t *testing.T) {
	r, board, clock := dcmiRig(t)
	board.powerErrs = []error{completionError{code: completionInitializing}}

	md := r.scrape(t)
	assert.Equal(t, 0, pointCount(md, "hw.host.power"))

	clock.Advance(5 * time.Minute)
	md = r.scrape(t)
	assert.Equal(t, 2, board.powerReads)
	assert.Equal(t, 1, pointCount(md, "hw.host.power"))
}

func TestInsufficientPrivilegeWarnsOnceAndStopsPowerReads(t *testing.T) {
	r, board, clock := dcmiRig(t)
	board.powerErrs = []error{completionError{code: completionInsufficientPrivilege}}

	r.scrape(t)
	clock.Advance(time.Hour)
	r.scrape(t)
	clock.Advance(time.Hour)
	r.scrape(t)

	assert.Equal(t, 1, board.powerReads)
	warns := r.logs.FilterLevelExact(zapcore.WarnLevel).FilterMessageSnippet("not an administrator")
	assert.Equal(t, 1, warns.Len())
}

func TestAMalformedDCMIReplyWarnsOncePerBMCAndIsRetriedAtTheNextInterval(t *testing.T) {
	r, board, clock := dcmiRig(t)
	board.powerErrs = []error{errMalformed, errMalformed, errMalformed}

	md := r.scrape(t)
	assert.Equal(t, 0, pointCount(md, "hw.host.power"), "no point for the interval")
	up, _ := upValue(t, md)
	assert.Equal(t, int64(1), up, "the sensors are fine")
	clock.Advance(5 * time.Minute)
	r.scrape(t)
	clock.Advance(5 * time.Minute)
	r.scrape(t)
	clock.Advance(5 * time.Minute)
	md = r.scrape(t)

	assert.Equal(t, 4, board.powerReads, "tried again at every power interval")
	assert.Equal(t, 1, pointCount(md, "hw.host.power"), "the first good answer gives a point")
	warns := r.logs.FilterLevelExact(zapcore.WarnLevel).FilterMessageSnippet("malformed")
	assert.Equal(t, 1, warns.Len(), "one WARN for the BMC, not one per interval")
}

func TestAdministratorDialFailureDoesNotFailTheSensorScrape(t *testing.T) {
	r, board, _ := dcmiRig(t)
	board.adminErr = errTimeout

	md := r.scrape(t)

	up, _ := upValue(t, md)
	assert.Equal(t, int64(1), up)
	assert.Equal(t, 1, pointCount(md, "hw.temperature"))
	assert.Equal(t, 0, pointCount(md, "hw.host.power"))
}

func TestAnAdministratorAuthFailureCountsAndBacksOffLikeAnyOther(t *testing.T) {
	r, board, clock := dcmiRig(t)
	board.adminErr = errAuth

	r.scrape(t)

	assert.Equal(t, []string{"pve12"}, r.authFails)
	clock.Advance(time.Minute)
	r.scrape(t)
	assert.Equal(t, []string{"user", "administrator", "user"}, board.dials, "the power session is not retried inside the back-off")
}

func TestARoleRefusalOnTheAdministratorSessionStopsPowerReadsAndIsNoCredentialFailure(t *testing.T) {
	r, board, clock := dcmiRig(t)
	board.adminErr = fmt.Errorf("%w: %w: level refused", errAuth, errRole)

	md := r.scrape(t)

	up, _ := upValue(t, md)
	assert.Equal(t, int64(1), up)
	assert.Empty(t, r.authFails, "the account is fine; it only lacks the administrator role")
	clock.Advance(time.Hour)
	r.scrape(t)
	assert.Equal(t, []string{"user", "administrator", "user"}, board.dials, "power reads stop after the first refusal")
}

// Secrets.

func TestPasswordNeverReachesALogLine(t *testing.T) {
	for name, err := range map[string]error{
		"auth":    fmt.Errorf("rakp failed for password secret: %w", errAuth),
		"refused": fmt.Errorf("dial secret: %w", errRefused),
		"other":   errors.New("login with secret failed"),
	} {
		t.Run(name, func(t *testing.T) {
			clock := newClock()
			board := labBoard(clock, 1, 0, 0)
			board.dialErr = err
			r := newRig(t, board, clock, nil)

			r.scrape(t)

			require.NotZero(t, r.logs.Len())
			for _, e := range r.logs.All() {
				assert.NotContains(t, e.Message, "secret")
				for _, f := range e.Context {
					assert.NotContains(t, fmt.Sprint(f.String, f.Interface), "secret")
				}
			}
		})
	}
}

func TestPasswordIsRedactedOnAGoodScrapeToo(t *testing.T) {
	clock := newClock()
	r := newRig(t, labBoard(clock, 1, 0, 0), clock, nil)

	r.scrape(t)

	for _, e := range r.logs.All() {
		assert.NotContains(t, e.Message, "secret")
	}
}
