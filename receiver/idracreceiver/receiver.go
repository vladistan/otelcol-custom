// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package idracreceiver

import (
	"context"
	"fmt"
	"hash/fnv"
	"slices"
	"sync"
	"time"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/receiver"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"
)

const (
	authFailuresMetric = "otelcol_hw_bmc_auth_failures"
	skippedReadsMetric = "otelcol_hw_bmc_skipped_reads"
	logoutTimeout      = 5 * time.Second
)

// startOffset spreads the first scrapes of the targets over the interval, by a
// hash of the name.
func startOffset(name string, interval time.Duration) time.Duration {
	h := fnv.New32a()
	_, _ = h.Write([]byte(name))
	return time.Duration(int64(h.Sum32()) % int64(interval))
}

type idracReceiver struct {
	logger   *zap.Logger
	interval time.Duration
	consumer consumer.Metrics
	targets  []*target
	offset   func(name string, interval time.Duration) time.Duration

	cancel context.CancelFunc
	wg     sync.WaitGroup
	sem    chan struct{} // limits the base scrapes
	// storageSem limits the storage reads on its own, so a slow storage read never holds a slot a base scrape needs.
	storageSem chan struct{}
}

func newReceiver(settings receiver.Settings, cfg *Config, next consumer.Metrics) (*idracReceiver, error) {
	counter, err := settings.MeterProvider.Meter(scopeName).Int64Counter(authFailuresMetric,
		metric.WithDescription("Failed BMC logins, by target"), metric.WithUnit("{login}"))
	if err != nil {
		return nil, fmt.Errorf("create %s: %w", authFailuresMetric, err)
	}
	skips, err := settings.MeterProvider.Meter(scopeName).Int64Counter(skippedReadsMetric,
		metric.WithDescription("Parts of a BMC read left out because the BMC answered an error, by target and part"), metric.WithUnit("{read}"))
	if err != nil {
		return nil, fmt.Errorf("create %s: %w", skippedReadsMetric, err)
	}
	r := &idracReceiver{
		logger: settings.Logger, interval: cfg.CollectionInterval, consumer: next, offset: startOffset,
		sem: make(chan struct{}, cfg.MaxConcurrent), storageSem: make(chan struct{}, cfg.MaxConcurrent),
	}
	e := &env{
		now: time.Now, logger: settings.Logger,
		authFailed: func(name string) {
			counter.Add(context.Background(), 1, metric.WithAttributes(attribute.String("bmc", name)))
		},
		skipped: func(name, part string) {
			skips.Add(context.Background(), 1, metric.WithAttributes(attribute.String("bmc", name), attribute.String("part", part)))
		},
	}
	for _, tc := range cfg.Targets {
		t, err := newTarget(tc.resolved(), e)
		if err != nil {
			return nil, err
		}
		r.targets = append(r.targets, t)
	}
	return r, nil
}

func (r *idracReceiver) Start(ctx context.Context, _ component.Host) error {
	ctx, r.cancel = context.WithCancel(ctx)
	for _, t := range r.targets {
		r.wg.Add(1)
		go r.run(ctx, t)
		if slices.Contains(t.cfg.Resources, "storage") {
			r.wg.Add(1)
			go r.runStorage(ctx, t)
		}
	}
	r.logger.Info("idrac receiver started", zap.Int("targets", len(r.targets)), zap.Duration("collection_interval", r.interval))
	return nil
}

// Shutdown stops the scrapes and deletes every Redfish session.
func (r *idracReceiver) Shutdown(ctx context.Context) error {
	if r.cancel != nil {
		r.cancel()
	}
	r.wg.Wait()
	for _, t := range r.targets {
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), logoutTimeout)
		t.close(cctx)
		cancel()
	}
	return nil
}

func (r *idracReceiver) run(ctx context.Context, t *target) {
	defer r.wg.Done()
	timer := time.NewTimer(r.offset(t.cfg.Name, r.interval))
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			r.scrapeAndSend(ctx, t)
			timer.Reset(r.interval)
		}
	}
}

// runStorage reads the storage resources of a target on its own timer, apart from the base scrape.
func (r *idracReceiver) runStorage(ctx context.Context, t *target) {
	defer r.wg.Done()
	every := t.cfg.storageInterval(r.interval)
	timer := time.NewTimer(r.offset(t.cfg.Name, r.interval))
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			r.storageAndSend(ctx, t)
			timer.Reset(every)
		}
	}
}

func (r *idracReceiver) scrapeAndSend(ctx context.Context, t *target) {
	r.send(ctx, t, t.scrape, r.sem)
}

func (r *idracReceiver) storageAndSend(ctx context.Context, t *target) {
	r.send(ctx, t, t.scrapeStorage, r.storageSem)
}

// send runs one read under the concurrency limit sem and passes the points on; a read with no points sends nothing.
func (r *idracReceiver) send(ctx context.Context, t *target, read func(context.Context) pmetric.Metrics, sem chan struct{}) {
	select {
	case sem <- struct{}{}:
	case <-ctx.Done():
		return
	}
	md := read(ctx)
	<-sem
	if ctx.Err() != nil || md.DataPointCount() == 0 {
		return
	}
	if err := r.consumer.ConsumeMetrics(ctx, md); err != nil {
		r.logger.Error("Failed to send idrac metrics", zap.String("target", t.cfg.Name), zap.Error(err))
	}
}
