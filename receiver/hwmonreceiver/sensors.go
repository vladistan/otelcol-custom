// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package hwmonreceiver

import "errors"

type kind int

const (
	kindTemperature kind = iota
	kindFan
	kindVoltage
	kindPowerSupply
)

// hwTypes holds the OpenTelemetry hw.type value of each sensor kind.
var hwTypes = map[kind]string{
	kindTemperature: "temperature",
	kindFan:         "fan",
	kindVoltage:     "voltage",
	kindPowerSupply: "power_supply",
}

// hwTypeOf returns the hw.type of a sensor kind; ok is false for a kind with no value.
func hwTypeOf(k kind) (string, bool) {
	v, ok := hwTypes[k]
	return v, ok
}

// limit is one limit of a temperature sensor, in degrees Celsius.
type limit struct {
	limitType string
	celsius   float64
}

// reading is one sensor at one scrape. The value is in Celsius, rpm or volts.
type reading struct {
	kind   kind
	id     string // stable across scrapes: <chip>/<file stem>
	name   string
	parent string // the chip, or the zone type
	value  float64
	limits []limit
}

// errNoSensors means the host has no sensor the options select.
var errNoSensors = errors.New("no sensors found")

// source reads every selected sensor once; tests replace it.
type source interface {
	read(opts readOptions) ([]reading, error)
}

// Values of hw.limit_type.
const (
	limitMax          = "max"
	limitCritical     = "critical"
	limitLowDegraded  = "low.degraded"
	limitLowCritical  = "low.critical"
	limitThrottled    = "throttled"
	limitHighDegraded = "high.degraded"
)

// celsiusFromTenthsKelvin converts a FreeBSD IK sysctl value.
func celsiusFromTenthsKelvin(v uint32) float64 {
	return float64(v)/10 - 273.15
}
