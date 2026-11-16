// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package smartreceiver

import (
	"context"
	"sync"
	"time"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/receiver"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"
)

// firstReadDelay is the wait between Start and the first read of the disks. Start itself sends no
// command, and the wait keeps a boot from reading every disk while the host is still busy.
const firstReadDelay = 5 * time.Second

type smartReceiver struct {
	logger     *zap.Logger
	interval   time.Duration
	firstDelay time.Duration
	consumer   consumer.Metrics
	scraper    *scraper

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func newReceiver(settings receiver.Settings, cfg *Config, next consumer.Metrics, b bus, skipped metric.Int64Counter, firstDelay time.Duration) (*smartReceiver, error) {
	s, err := newScraper(settings.Logger, cfg, b, skipped, time.Now())
	if err != nil {
		return nil, err
	}
	return &smartReceiver{
		logger:     settings.Logger,
		interval:   cfg.CollectionInterval,
		firstDelay: firstDelay,
		consumer:   next,
		scraper:    s,
	}, nil
}

// Start begins the read loop; it sends no command to any disk.
func (r *smartReceiver) Start(ctx context.Context, _ component.Host) error {
	ctx, r.cancel = context.WithCancel(ctx)
	r.wg.Add(1)
	go r.collect(ctx)
	r.logger.Info("smart receiver started", zap.Duration("collection_interval", r.interval))
	return nil
}

// Shutdown stops the loop and waits for the device in hand, at most until ctx ends. The loop reads no
// further device after the cancel, so the wait is one command timeout, not one per device.
func (r *smartReceiver) Shutdown(ctx context.Context) error {
	if r.cancel != nil {
		r.cancel()
	}
	done := make(chan struct{})
	go func() {
		r.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *smartReceiver) collect(ctx context.Context) {
	defer r.wg.Done()
	first := time.NewTimer(r.firstDelay)
	defer first.Stop()
	select {
	case <-ctx.Done():
		return
	case <-first.C:
	}
	r.scrapeAndSend(ctx)

	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.scrapeAndSend(ctx)
		}
	}
}

func (r *smartReceiver) scrapeAndSend(ctx context.Context) {
	md := r.scraper.scrape(ctx, time.Now())
	if md.MetricCount() == 0 {
		return
	}
	if err := r.consumer.ConsumeMetrics(ctx, md); err != nil {
		r.logger.Error("Failed to send smart metrics", zap.Error(err))
	}
}
