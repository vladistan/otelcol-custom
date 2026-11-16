// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package ipmireceiver

import (
	"context"
	"errors"
	"fmt"
	"time"
)

type sensorKind int

// The sensor kinds the receiver reports; anything else is kindOther and is not read.
const (
	kindOther sensorKind = iota
	kindTemperature
	kindFan
	kindVoltage
	kindPowerSupply
	kindIntrusion
)

// sdrRecord is one sensor of the SDR, with what the receiver needs to read and
// report it. handle belongs to the adapter: it carries the conversion factors.
type sdrRecord struct {
	number           uint8
	name             string
	kind             sensorKind
	upperCritical    *float64 // degrees Celsius, temperature sensors only
	upperNonCritical *float64
	handle           any
}

// sdrSet is the sensor part of the SDR repository and its timestamp.
type sdrSet struct {
	stamp   time.Time
	records []sdrRecord
}

// sensorReading is one reading: a converted value for an analog sensor, the
// asserted state offsets for a discrete one.
type sensorReading struct {
	value  float64
	active []uint8
}

type deviceInfo struct{ firmware string }

// IPMI completion codes the receiver reacts to.
const (
	ccInvalidCommand        = 0xC1
	ccInitializing          = 0xD2
	ccInsufficientPrivilege = 0xD4
)

// completionError is a non-zero completion code of a BMC answer.
type completionError struct{ code uint8 }

func (e completionError) Error() string { return fmt.Sprintf("ipmi completion code 0x%02x", e.code) }

// The adapter maps what the transport reports to these.
var (
	errNotPresent = errors.New("sensor not present")
	errAuth       = errors.New("authentication failed")
	// errRole marks an authentication failure caused by the role: the account is known but may not
	// have the privilege level asked for. Errors that carry it also carry errAuth.
	errRole    = errors.New("role refused")
	errRefused = errors.New("connection refused")
	errTimeout = errors.New("timed out")
)

// bmcSession is the whole of what the receiver asks of a BMC: the four reads
// and the close. The adapter (adapter.go) is the only file that speaks go-ipmi.
type bmcSession interface {
	GetDeviceID(ctx context.Context) (deviceInfo, error)
	// GetSDRsMap reads the sensor records unless the repository timestamp equals
	// known (a zero known always reads); unchanged is true when it does.
	GetSDRsMap(ctx context.Context, known time.Time) (set sdrSet, unchanged bool, err error)
	GetSensorReading(ctx context.Context, rec sdrRecord) (sensorReading, error)
	GetDCMIPowerReading(ctx context.Context) (watts float64, err error)
	Close(ctx context.Context) error
}

// dialFunc opens one session at a privilege level (user, operator or administrator).
type dialFunc func(ctx context.Context, cfg BMCConfig, level string) (bmcSession, error)
