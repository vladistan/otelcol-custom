// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package ipmireceiver

import (
	"context"
	"errors"
	"net"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.uber.org/zap"

	"github.com/vladistan/otelcol-custom/receiver/hwschema"
)

const (
	scopeName = "github.com/vladistan/otelcol-custom/receiver/ipmireceiver"
	// sdrMaxAge is how long a cached SDR is trusted without a full read.
	sdrMaxAge = 6 * time.Hour
	// maxAuthFailures in a row stop a BMC until the receiver restarts.
	maxAuthFailures = 3
	maxAuthBackoff  = 6 * time.Hour
	closeTimeout    = 5 * time.Second
)

// env is what every target shares; tests replace the clock and the dialer.
type env struct {
	now           func() time.Time
	dial          dialFunc
	logger        *zap.Logger
	scrapeTimeout time.Duration
	authFailed    func(bmc string)
}

// target is the state of one BMC between scrapes.
type target struct {
	cfg BMCConfig
	env *env

	authFailures int
	retryAt      time.Time
	halted       bool

	sdr     sdrSet
	sdrAt   time.Time
	haveSDR bool

	dcmiAbsent    bool // the BMC answered invalid command: no DCMI power
	powerStopped  bool // the account is not an administrator, or too many logins failed
	nextPower     time.Time
	powerFailures int
	firmware      string
}

func newTarget(cfg BMCConfig, e *env) *target { return &target{cfg: cfg, env: e} }

// authBackoff is the silence after the nth authentication failure in a row: 15
// minutes, doubling, to 6 hours.
func authBackoff(n int) time.Duration {
	d := 15 * time.Minute
	for i := 1; i < n && d < maxAuthBackoff; i++ {
		d *= 2
	}
	return min(d, maxAuthBackoff)
}

func (t *target) redact(s string) string {
	if t.cfg.Password == "" {
		return s
	}
	return strings.ReplaceAll(s, string(t.cfg.Password), "[redacted]")
}

func (t *target) resource() hwschema.Resource {
	host := t.cfg.Address
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return hwschema.Resource{
		HostName: t.cfg.Name, Vendor: t.cfg.Vendor, ServerAddress: host, Protocol: hwschema.ProtocolIPMI, Firmware: t.firmware,
	}
}

// scrape reads the BMC once. A failed scrape holds only hw.bmc.up 0 and the
// duration, so a value of an earlier scrape is never sent again.
func (t *target) scrape(ctx context.Context) pmetric.Metrics {
	start := t.env.now()
	if t.halted || start.Before(t.retryAt) {
		return t.failed(start, hwschema.ReasonBackoff)
	}
	ctx, cancel := context.WithTimeout(ctx, t.env.scrapeTimeout)
	defer cancel()

	conn, err := t.env.dial(ctx, t.cfg, t.cfg.PrivilegeLevel)
	if err != nil {
		return t.failed(start, t.classify(err, start))
	}
	points, err := t.read(ctx, conn, start)
	t.closeSession(ctx, conn)
	if err != nil {
		return t.failed(start, t.classify(err, start))
	}
	t.authFailures = 0

	b := hwschema.NewBuilder(scopeName, t.resource(), start)
	for _, p := range points {
		p(b)
	}
	t.readPower(ctx, b, start)
	b.BMCUp(true, "")
	b.BMCScrapeDuration(t.env.now().Sub(start))
	return b.Metrics()
}

func (t *target) failed(start time.Time, reason string) pmetric.Metrics {
	b := hwschema.NewBuilder(scopeName, t.resource(), start)
	b.BMCUp(false, reason)
	b.BMCScrapeDuration(t.env.now().Sub(start))
	return b.Metrics()
}

// classify turns an error into a reason, and applies the authentication back-off.
func (t *target) classify(err error, now time.Time) string {
	msg := t.redact(err.Error())
	switch {
	case errors.Is(err, errAuth):
		t.authFailures++
		t.env.authFailed(t.cfg.Name)
		if t.authFailures >= maxAuthFailures {
			t.halted = true
			t.env.logger.Warn("BMC authentication failed three times; not contacting it again until restart",
				zap.String("bmc", t.cfg.Name), zap.String("error", msg))
		} else {
			t.retryAt = now.Add(authBackoff(t.authFailures))
			t.env.logger.Warn("BMC authentication failed",
				zap.String("bmc", t.cfg.Name), zap.Duration("retry_after", authBackoff(t.authFailures)), zap.String("error", msg))
		}
		return hwschema.ReasonAuth
	case errors.Is(err, errTimeout), errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		t.env.logger.Debug("BMC scrape timed out", zap.String("bmc", t.cfg.Name), zap.String("error", msg))
		return hwschema.ReasonTimeout
	default:
		t.env.logger.Debug("BMC scrape failed", zap.String("bmc", t.cfg.Name), zap.String("error", msg))
		return hwschema.ReasonRefused
	}
}

func (t *target) closeSession(ctx context.Context, conn bmcSession) {
	closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), closeTimeout)
	defer cancel()
	if err := conn.Close(closeCtx); err != nil {
		t.env.logger.Debug("BMC session close failed", zap.String("bmc", t.cfg.Name), zap.String("error", t.redact(err.Error())))
	}
}

// read gets the device ID, the SDR and the selected sensors, and returns what
// to emit; it fails only for an error that ends the session.
func (t *target) read(ctx context.Context, conn bmcSession, now time.Time) ([]func(*hwschema.Builder), error) {
	dev, err := conn.GetDeviceID(ctx)
	if err != nil {
		return nil, err
	}
	t.firmware = dev.firmware

	var known time.Time
	if t.haveSDR && now.Sub(t.sdrAt) < sdrMaxAge {
		known = t.sdr.stamp
	}
	set, unchanged, err := conn.GetSDRsMap(ctx, known)
	if err != nil {
		return nil, err
	}
	if !unchanged {
		t.sdr, t.sdrAt, t.haveSDR = set, now, true
	}

	var points []func(*hwschema.Builder)
	for _, rec := range t.sdr.records {
		if rec.kind == kindOther {
			continue
		}
		reading, err := conn.GetSensorReading(ctx, rec)
		if err != nil {
			if errors.Is(err, errNotPresent) || isCompletion(err) {
				t.env.logger.Debug("Sensor has no reading", zap.String("bmc", t.cfg.Name), zap.String("sensor", rec.name))
				continue
			}
			return nil, err
		}
		if p := t.point(rec, reading); p != nil {
			points = append(points, p)
		}
	}
	return points, nil
}

func isCompletion(err error) bool {
	var ce completionError
	return errors.As(err, &ce)
}

func sensorID(prefix string, rec sdrRecord) string {
	return prefix + "/" + strconv.Itoa(int(rec.number))
}

// point maps one reading to its metrics; a reading that means nothing gives nil.
func (t *target) point(rec sdrRecord, r sensorReading) func(*hwschema.Builder) {
	parent := t.cfg.Name
	switch rec.kind {
	case kindTemperature:
		id := sensorID("temperature", rec)
		return func(b *hwschema.Builder) {
			b.Temperature(id, rec.name, parent, r.value)
			if rec.upperCritical != nil {
				b.TemperatureLimit(id, rec.name, parent, hwschema.LimitCritical, *rec.upperCritical)
			}
			if rec.upperNonCritical != nil {
				b.TemperatureLimit(id, rec.name, parent, hwschema.LimitHighDegraded, *rec.upperNonCritical)
			}
		}
	case kindFan:
		return func(b *hwschema.Builder) { b.FanSpeed(sensorID("fan", rec), rec.name, parent, r.value) }
	case kindVoltage:
		return func(b *hwschema.Builder) { b.Voltage(sensorID("voltage", rec), rec.name, parent, r.value) }
	case kindPowerSupply:
		state, ok := powerSupplyState(r.active)
		if !ok {
			return nil
		}
		return func(b *hwschema.Builder) {
			b.Status(hwschema.TypePowerSupply, sensorID("power_supply", rec), rec.name, state)
		}
	case kindIntrusion:
		state := hwschema.StateOK
		if has(r.active, 0) {
			state = hwschema.StateFailed
		}
		return func(b *hwschema.Builder) {
			b.Status(hwschema.TypeEnclosure, sensorID("intrusion", rec), "chassis intrusion", state)
		}
	}
	return nil
}

func has(active []uint8, offset uint8) bool {
	for _, a := range active {
		if a == offset {
			return true
		}
	}
	return false
}

// powerSupplyState maps the asserted offsets of a Power Supply sensor: 0
// presence, 1 failure, 2 predictive failure, 3 and 4 input lost, 5 input out of
// range, 6 configuration error. A supply that is not present has no state.
func powerSupplyState(active []uint8) (string, bool) {
	if !has(active, 0) {
		return "", false
	}
	switch {
	case has(active, 1), has(active, 3), has(active, 4):
		return hwschema.StateFailed, true
	case has(active, 2):
		return hwschema.StatePredictedFailure, true
	case has(active, 5), has(active, 6):
		return hwschema.StateDegraded, true
	}
	return hwschema.StateOK, true
}

// readPower reads the DCMI power over a separate administrator session, once
// per power_interval. Nothing here fails the scrape.
func (t *target) readPower(ctx context.Context, b *hwschema.Builder, now time.Time) {
	if !t.cfg.DCMIPower || t.dcmiAbsent || t.powerStopped || now.Before(t.nextPower) {
		return
	}
	t.nextPower = now.Add(t.cfg.PowerInterval)
	conn, err := t.env.dial(ctx, t.cfg, "administrator")
	if err != nil {
		if errors.Is(err, errRole) {
			t.powerStopped = true
			t.env.logger.Warn("the account cannot open an administrator session; DCMI power reads stop until restart",
				zap.String("bmc", t.cfg.Name), zap.String("error", t.redact(err.Error())))
			return
		}
		if errors.Is(err, errAuth) {
			t.powerAuthFailed(now, err)
			return
		}
		t.env.logger.Debug("DCMI power session failed", zap.String("bmc", t.cfg.Name), zap.String("error", t.redact(err.Error())))
		return
	}
	watts, err := conn.GetDCMIPowerReading(ctx)
	t.closeSession(ctx, conn)
	if err == nil {
		t.powerFailures = 0
		b.HostPower(watts)
		return
	}
	var ce completionError
	switch {
	case errors.As(err, &ce) && ce.code == ccInvalidCommand:
		t.dcmiAbsent = true
		t.env.logger.Info("BMC has no DCMI power; not asking again", zap.String("bmc", t.cfg.Name))
	case errors.As(err, &ce) && ce.code == ccInitializing:
		t.env.logger.Debug("BMC is initializing; DCMI power is tried again at the next interval", zap.String("bmc", t.cfg.Name))
	case errors.As(err, &ce) && ce.code == ccInsufficientPrivilege:
		t.powerStopped = true
		t.env.logger.Warn("DCMI power needs an administrator account and this one is not an administrator; power reads stop until restart",
			zap.String("bmc", t.cfg.Name))
	default:
		t.env.logger.Debug("DCMI power read failed", zap.String("bmc", t.cfg.Name), zap.String("error", t.redact(err.Error())))
	}
}

// powerAuthFailed counts a failed administrator login and backs the power
// reads off like the sensor session; sensors keep being read.
func (t *target) powerAuthFailed(now time.Time, err error) {
	t.powerFailures++
	t.env.authFailed(t.cfg.Name)
	if t.powerFailures >= maxAuthFailures {
		t.powerStopped = true
	} else {
		t.nextPower = now.Add(authBackoff(t.powerFailures))
	}
	t.env.logger.Warn("BMC administrator authentication failed", zap.String("bmc", t.cfg.Name), zap.String("error", t.redact(err.Error())))
}
