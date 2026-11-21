// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package ipmireceiver

import (
	"context"
	"net"
	"syscall"
	"testing"
	"time"

	"github.com/bougou/go-ipmi/pkg/types"
	"github.com/stretchr/testify/assert"
)

// The error a mistyped user name gives, in the shape go-ipmi's Connect returns it, backs off and
// stops after three failures like a wrong password.
func TestAWrongUserNameInTheConnectErrorBacksOffAndHalts(t *testing.T) {
	clock := newClock()
	board := labBoard(clock, 1, 0, 0)
	board.dialErr = classify(connect20Error("rakp1", types.NewRmcpStatusError(types.RmcpStatusCodeUnauthorizedName)))
	r := newRig(t, board, clock, nil)

	r.scrape(t)
	clock.Advance(time.Minute)
	r.scrape(t)
	assert.Len(t, board.dials, 1, "no second login inside the 15 minute back-off")
	clock.Advance(15 * time.Minute)
	r.scrape(t)
	clock.Advance(30 * time.Minute)
	r.scrape(t)
	clock.Advance(24 * time.Hour)
	r.scrape(t)

	assert.Len(t, board.dials, 3, "three logins, then none until restart")
	assert.Equal(t, []string{"node12", "node12", "node12"}, r.authFails)
}

// A role refusal on the sensor session is a login failure and backs off the same way.
func TestARoleRefusalOnTheSensorSessionBacksOff(t *testing.T) {
	clock := newClock()
	board := labBoard(clock, 1, 0, 0)
	board.dialErr = classify(connect20Error("RMCP+ Open Session", types.NewRmcpStatusError(types.RmcpStatusCodeInvalidRole)))
	r := newRig(t, board, clock, nil)

	r.scrape(t)
	clock.Advance(time.Minute)
	r.scrape(t)

	assert.Len(t, board.dials, 1)
	assert.Equal(t, []string{"node12"}, r.authFails)
}

// A timeout or a refused connection inside the Set Session Privilege Level step is a network
// failure: no login failure is counted, no back-off starts, and a later scrape dials again.
func TestATimeoutInThePrivilegeStepOfTheSensorSessionIsNoLoginFailure(t *testing.T) {
	for name, inner := range map[string]error{
		"timeout": timeoutError{}, "deadline": context.DeadlineExceeded,
		"refused": &net.OpError{Op: "read", Err: syscall.ECONNREFUSED},
	} {
		t.Run(name, func(t *testing.T) {
			clock := newClock()
			board := labBoard(clock, 1, 0, 0)
			board.dialErr = classify(privilegeLevelError(inner))
			r := newRig(t, board, clock, nil)

			for range 4 {
				r.scrape(t)
				clock.Advance(time.Minute)
			}

			assert.Len(t, board.dials, 4, "a dial in every scrape: no back-off, no halt")
			assert.Empty(t, r.authFails)
		})
	}
}

// One timeout in the privilege step of the administrator session does not end the power reads.
func TestATimeoutInThePrivilegeStepOfTheAdministratorSessionKeepsThePowerReadsGoing(t *testing.T) {
	r, board, clock := dcmiRig(t)
	board.adminErr = classify(privilegeLevelError(timeoutError{}))

	r.scrape(t)
	board.adminErr = nil
	clock.Advance(time.Hour)
	r.scrape(t)

	assert.Empty(t, r.authFails)
	assert.Equal(t, []string{"user", "administrator", "user", "administrator"}, board.dials)
	assert.Equal(t, 1, board.powerReads, "the second administrator session reads the power")
}
