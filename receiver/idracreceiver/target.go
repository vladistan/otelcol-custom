// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package idracreceiver

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"runtime/debug"
	"sync"
	"time"

	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.uber.org/zap"

	"github.com/vladistan/otelcol-custom/receiver/hwschema"
)

const (
	scopeName       = "github.com/vladistan/otelcol-custom/receiver/idracreceiver"
	maxAuthFailures = 3
	maxAuthBackoff  = 6 * time.Hour

	// A storage read has scrape_timeout plus defaultDrivePace for each drive it finds, up to maxStorageReadTime.
	// The iDRAC answers one drive in about 0.4 s, so a pace of 1 s covers a slow one. The default 256 drives
	// give 30 s + 256 s.
	defaultDrivePace   = time.Second
	maxStorageReadTime = 10 * time.Minute
)

// env is what every target shares; tests replace the clock.
type env struct {
	now        func() time.Time
	logger     *zap.Logger
	authFailed func(target string)
	skipped    func(target, part string) // a member was left out of a scrape; may be nil
}

// target is one iDRAC and its state between scrapes.
type target struct {
	cfg     TargetConfig
	env     *env
	client  *client
	profile hwschema.RedfishProfile

	authFailures int
	retryAt      time.Time
	halted       bool
	info         bmcInfo

	mu              sync.Mutex // guards info, noExpandUntil and the state the storage read shares with the base scrape
	noExpandUntil   time.Time  // the BMC refused $expand; the query is sent again after this time
	basePanicLogged bool
	tlsWarned       bool            // the certificate warning of the current episode was logged
	warned          map[string]bool // soft failures warned about in the last scrape
	softNow         map[string]bool // soft failures of the scrape that is running

	storage  storageState       // the state of the storage read, which only that read uses
	onDecode func(group string) // called before a storage member is decoded; a test sets it

	// drivePace is the time added to the storage read budget for each drive; readCeiling limits the budget.
	drivePace   time.Duration
	readCeiling time.Duration
}

func (t *target) noExpandUntilTime() time.Time {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.noExpandUntil
}

func (t *target) setNoExpandUntil(at time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.noExpandUntil = at
}

// isBlocked reports whether the base scrape has stopped contacting the BMC: halted after three failed logins,
// or inside a back-off. The storage read follows it.
func (t *target) isBlocked(now time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.halted || now.Before(t.retryAt)
}

func newTarget(cfg TargetConfig, e *env) (*target, error) {
	c, err := newClient(cfg, e.logger.With(zap.String("target", cfg.Name)), e.now)
	if err != nil {
		return nil, err
	}
	profile, ok := hwschema.RedfishProfileFor("dell")
	if !ok {
		return nil, errors.New("hwschema has no Redfish profile for dell")
	}
	return &target{
		cfg: cfg, env: e, client: c, profile: profile, warned: map[string]bool{},
		drivePace: defaultDrivePace, readCeiling: maxStorageReadTime,
	}, nil
}

// authBackoff is the silence after the nth authentication failure in a row: 15
// minutes, doubling, to 6 hours.
func authBackoff(n int) time.Duration {
	d := 15 * time.Minute
	for i := 1; i < n && d < maxAuthBackoff; i++ {
		d *= 2
	}
	return min(d, maxAuthBackoff)
}

// storageResource names the BMC without what the base scrape learns (model, serial, firmware), so the storage
// series keep one resource while the base scrape reads those.
func (t *target) storageResource() hwschema.Resource {
	host := t.cfg.Address
	if u, err := url.Parse(host); err == nil && u.Hostname() != "" {
		host = u.Hostname()
	}
	return hwschema.Resource{HostName: t.cfg.Name, Vendor: "dell", ServerAddress: host, Protocol: hwschema.ProtocolRedfish}
}

func (t *target) resource() hwschema.Resource {
	res := t.storageResource()
	res.Model, res.Serial, res.Firmware = t.info.model, t.info.serial, t.info.firmware
	return res
}

// scrape reads the iDRAC once. A failed scrape holds only hw.bmc.up 0 and the
// duration, so a value of an earlier scrape is never sent again.
func (t *target) scrape(ctx context.Context) pmetric.Metrics {
	start := t.env.now()
	if t.isBlocked(start) {
		return t.failed(start, hwschema.ReasonBackoff)
	}
	ctx, cancel := context.WithTimeout(ctx, t.cfg.ScrapeTimeout)
	defer cancel()

	t.softNow = map[string]bool{}
	points, inf, err := t.safeCollect(ctx)
	if err != nil {
		return t.failed(start, t.classify(err, start))
	}
	t.authFailures = 0
	t.tlsWarned = false
	t.warned = t.softNow
	t.info = inf

	b := hwschema.NewBuilder(scopeName, t.resource(), start)
	for _, p := range points {
		p(b)
	}
	b.BMCUp(true, "")
	b.BMCScrapeDuration(t.env.now().Sub(start))
	return b.Metrics()
}

// safeCollect is collect with a panic turned into an error, logged once for the target. The decoders read
// bodies from the BMC, and a panic in a receiver goroutine would end the whole collector.
func (t *target) safeCollect(ctx context.Context) (points []pointAdder, inf bmcInfo, err error) {
	defer func() {
		if p := recover(); p != nil {
			if !t.basePanicLogged {
				t.basePanicLogged = true
				t.env.logger.Error("iDRAC decoder failed; the scrape is skipped and the next one runs",
					zap.String("target", t.cfg.Name), zap.String("panic", t.client.redact(fmt.Sprint(p))), zap.ByteString("stack", debug.Stack()))
			}
			points, inf, err = nil, bmcInfo{}, errPanic
		}
	}()
	return t.collect(ctx)
}

func (t *target) failed(start time.Time, reason string) pmetric.Metrics {
	b := hwschema.NewBuilder(scopeName, t.resource(), start)
	b.BMCUp(false, reason)
	b.BMCScrapeDuration(t.env.now().Sub(start))
	return b.Metrics()
}

// classify turns an error into a reason and applies the authentication back-off.
func (t *target) classify(err error, now time.Time) string {
	msg := t.client.redact(err.Error())
	switch {
	case errors.Is(err, errAuth):
		t.authFailures++
		t.env.authFailed(t.cfg.Name)
		t.mu.Lock()
		defer t.mu.Unlock()
		if t.authFailures >= maxAuthFailures {
			t.halted = true
			t.env.logger.Warn("iDRAC authentication failed three times; not contacting it again until restart",
				zap.String("target", t.cfg.Name), zap.String("error", msg))
		} else {
			t.retryAt = now.Add(authBackoff(t.authFailures))
			t.env.logger.Warn("iDRAC authentication failed",
				zap.String("target", t.cfg.Name), zap.Duration("retry_after", authBackoff(t.authFailures)), zap.String("error", msg))
		}
		return hwschema.ReasonAuth
	case errors.Is(err, errPanic):
		return hwschema.ReasonError
	case errors.Is(err, errTLS):
		if t.tlsWarned {
			t.env.logger.Debug("iDRAC certificate was not verified", zap.String("target", t.cfg.Name), zap.String("error", msg))
		} else {
			t.tlsWarned = true
			t.env.logger.Warn("iDRAC certificate was not verified; set ca_file to its CA",
				zap.String("target", t.cfg.Name), zap.String("error", msg))
		}
		return hwschema.ReasonTLS
	case errors.Is(err, errTimeout), errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		t.env.logger.Debug("iDRAC scrape timed out", zap.String("target", t.cfg.Name), zap.String("error", msg))
		return hwschema.ReasonTimeout
	}
	t.env.logger.Debug("iDRAC scrape failed", zap.String("target", t.cfg.Name), zap.String("error", msg))
	return hwschema.ReasonRefused
}

// isSoft reports whether a failed read of one member is skipped: a missing path, an unexpected status or a
// body that does not decode. A timeout, login or TLS failure ends the scrape, as the BMC is out of reach.
// The first skip of a kind logs one warning until a scrape has none.
func (t *target) isSoft(kind, path string, err error) bool {
	switch {
	case errors.Is(err, errAbsent):
		return true
	case errors.Is(err, errStatus), errors.Is(err, errUndecodable):
		t.softNow[kind] = true
		if t.env.skipped != nil {
			t.env.skipped(t.cfg.Name, kind)
		}
		if !t.warned[kind] {
			t.warned[kind] = true
			t.env.logger.Warn("iDRAC answered an error for part of the read; that part is skipped",
				zap.String("target", t.cfg.Name), zap.String("part", kind), zap.String("path", path), zap.String("error", t.client.redact(err.Error())))
		}
		return true
	}
	return false
}

// skipUndecodable counts and warns about a member of kind whose body did not decode.
func (t *target) skipUndecodable(kind string, err error) {
	t.isSoft(kind, "", fmt.Errorf("%w: %v", errUndecodable, err))
}

func (t *target) close(ctx context.Context) { t.client.logout(ctx) }
