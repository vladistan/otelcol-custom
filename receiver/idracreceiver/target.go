// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package idracreceiver

import (
	"context"
	"errors"
	"net/url"
	"time"

	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.uber.org/zap"

	"github.com/vladistan/otelcol-custom/receiver/hwschema"
)

const (
	scopeName       = "github.com/vladistan/otelcol-custom/receiver/idracreceiver"
	maxAuthFailures = 3
	maxAuthBackoff  = 6 * time.Hour
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
	info         info

	noExpandUntil time.Time       // the BMC refused $expand; the query is sent again after this time
	tlsWarned     bool            // the certificate warning of the current episode was logged
	warned        map[string]bool // soft failures warned about in the last scrape
	softNow       map[string]bool // soft failures of the scrape that is running
}

func newTarget(cfg TargetConfig, e *env) (*target, error) {
	c, err := newClient(cfg, e.logger.With(zap.String("target", cfg.Name)), e.now)
	if err != nil {
		return nil, err
	}
	profile, _ := hwschema.RedfishProfileFor("dell")
	return &target{cfg: cfg, env: e, client: c, profile: profile, warned: map[string]bool{}}, nil
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

func (t *target) resource() hwschema.Resource {
	host := t.cfg.Address
	if u, err := url.Parse(host); err == nil && u.Hostname() != "" {
		host = u.Hostname()
	}
	return hwschema.Resource{
		HostName: t.cfg.Name, Vendor: "dell", ServerAddress: host, Protocol: hwschema.ProtocolRedfish,
		Model: t.info.model, Serial: t.info.serial, Firmware: t.info.firmware,
	}
}

// scrape reads the iDRAC once. A failed scrape holds only hw.bmc.up 0 and the
// duration, so a value of an earlier scrape is never sent again.
func (t *target) scrape(ctx context.Context) pmetric.Metrics {
	start := t.env.now()
	if t.halted || start.Before(t.retryAt) {
		return t.failed(start, hwschema.ReasonBackoff)
	}
	ctx, cancel := context.WithTimeout(ctx, t.cfg.ScrapeTimeout)
	defer cancel()

	t.softNow = map[string]bool{}
	points, inf, err := t.collect(ctx)
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

// soft decides whether a failed read of one member is skipped. A path the BMC does not serve and an
// unexpected HTTP status skip the member; a timeout, a login or TLS failure ends the scrape, because the
// BMC is then slow or out of reach. The first skip of a kind logs one warning until a scrape has none.
func (t *target) soft(kind, path string, err error) bool {
	switch {
	case errors.Is(err, errAbsent):
		return true
	case errors.Is(err, errStatus):
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

// close deletes the Redfish session.
func (t *target) close(ctx context.Context) { t.client.logout(ctx) }
