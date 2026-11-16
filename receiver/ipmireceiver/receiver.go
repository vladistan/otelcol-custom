// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package ipmireceiver

import (
	"context"
	"hash/fnv"
	"sync"
	"time"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/receiver"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"
)

const authFailuresMetric = "otelcol_hw_bmc_auth_failures"

// startOffset spreads the first scrapes of the BMCs over the interval, by a
// hash of the name, so the boards are not all hit in the same second.
func startOffset(name string, interval time.Duration) time.Duration {
	h := fnv.New32a()
	_, _ = h.Write([]byte(name))
	return time.Duration(int64(h.Sum32()) % int64(interval))
}

type ipmiReceiver struct {
	logger   *zap.Logger
	cfg      *Config
	consumer consumer.Metrics
	dial     dialFunc
	offset   func(name string, interval time.Duration) time.Duration
	authFail metric.Int64Counter

	sem    chan struct{}
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func newReceiver(settings receiver.Settings, cfg *Config, next consumer.Metrics, dial dialFunc) *ipmiReceiver {
	counter, err := settings.MeterProvider.Meter(scopeName).Int64Counter(authFailuresMetric,
		metric.WithDescription("Failed BMC logins, by BMC"), metric.WithUnit("{login}"))
	if err != nil {
		settings.Logger.Warn("Cannot create the authentication failure counter", zap.Error(err))
	}
	return &ipmiReceiver{
		logger: settings.Logger, cfg: cfg, consumer: next, dial: dial, offset: startOffset, authFail: counter,
		sem: make(chan struct{}, cfg.MaxConcurrent),
	}
}

func (r *ipmiReceiver) Start(ctx context.Context, _ component.Host) error {
	ctx, r.cancel = context.WithCancel(ctx)
	e := &env{
		now: time.Now, dial: r.dial, logger: r.logger, scrapeTimeout: r.cfg.ScrapeTimeout,
		authFailed: func(name string) {
			if r.authFail != nil {
				r.authFail.Add(ctx, 1, metric.WithAttributes(attribute.String("bmc", name)))
			}
		},
	}
	for _, b := range r.cfg.BMCs {
		b = b.resolved()
		if b.PrivilegeLevel != "user" {
			r.logger.Warn("The sensor session asks for more than the user privilege level; sensors and the SDR need only user, and an account with more rights than that is a larger risk if the password leaks",
				zap.String("bmc", b.Name), zap.String("privilege_level", b.PrivilegeLevel))
		}
		r.wg.Add(1)
		go r.run(ctx, newTarget(b, e))
	}
	r.logger.Info("ipmi receiver started", zap.Int("bmcs", len(r.cfg.BMCs)), zap.Duration("collection_interval", r.cfg.CollectionInterval))
	return nil
}

func (r *ipmiReceiver) Shutdown(context.Context) error {
	if r.cancel != nil {
		r.cancel()
	}
	r.wg.Wait()
	return nil
}

// run scrapes one BMC after its start offset and then at every interval.
func (r *ipmiReceiver) run(ctx context.Context, t *target) {
	defer r.wg.Done()
	timer := time.NewTimer(r.offset(t.cfg.Name, r.cfg.CollectionInterval))
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			r.scrapeAndSend(ctx, t)
			timer.Reset(r.cfg.CollectionInterval)
		}
	}
}

func (r *ipmiReceiver) scrapeAndSend(ctx context.Context, t *target) {
	select {
	case r.sem <- struct{}{}:
	case <-ctx.Done():
		return
	}
	md := t.scrape(ctx)
	<-r.sem
	if ctx.Err() != nil {
		return
	}
	if err := r.consumer.ConsumeMetrics(ctx, md); err != nil {
		r.logger.Error("Failed to send ipmi metrics", zap.String("bmc", t.cfg.Name), zap.Error(err))
	}
}
