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

// IPMI completion codes the receiver reacts to; 0x81 and 0x82 are the refusals of
// Set Session Privilege Level, 0xCB is "requested data not present".
const (
	completionRequestedLevelUnavailable = 0x81
	completionAboveChannelLimit         = 0x82
	completionNoData                    = 0xCB
	completionInvalidCommand            = 0xC1
	completionInitializing              = 0xD2
	completionInsufficientPrivilege     = 0xD4
)

// completionError stays typed so callers can branch on the code; the text alone would not do.
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
	// errMalformed marks a reply the BMC sent that is too short or is not a DCMI answer.
	errMalformed = errors.New("malformed reply")
)

// fruDevice is a FRU device the SDR lists: a DIMM or a processor.
type fruDevice struct {
	id   uint8
	name string
	kind string // hwschema.FRUDIMM or hwschema.FRUCPU
	// isRemote: another controller than the BMC holds the device; the adapter cannot bridge to it.
	isRemote bool
	// isSPD: the SDR says the device holds a DIMM memory ID (JEDEC SPD), not an IPMI FRU image.
	isSPD bool
}

// bmcSession is the whole of what the receiver asks of a BMC. The adapter is the
// only file that speaks go-ipmi.
type bmcSession interface {
	// ListFRUDevices lists the DIMM and processor FRU devices of the SDR, at most fruMaxDevices.
	ListFRUDevices(ctx context.Context) ([]fruDevice, error)
	// ReadFRU reads the areas of one FRU device; a device that is not there gives errNotPresent.
	ReadFRU(ctx context.Context, deviceID uint8) ([]byte, error)
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
