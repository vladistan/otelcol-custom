// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package ethtoolreceiver

import (
	"context"
	"sync"
	"time"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/receiver"
	"go.uber.org/zap"
)

type ethtoolReceiver struct {
	logger   *zap.Logger
	interval time.Duration
	consumer consumer.Metrics
	scraper  *scraper
	nic      nicReader

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func newReceiver(settings receiver.Settings, cfg *Config, next consumer.Metrics, nic nicReader) *ethtoolReceiver {
	return &ethtoolReceiver{
		logger:   settings.Logger,
		interval: cfg.CollectionInterval,
		consumer: next,
		scraper:  newScraper(settings.Logger, cfg, nic, counterStart(settings.Logger, nic)),
		nic:      nic,
	}
}

// counterStart is the host boot time, the start of the counters; an unknown
// boot time falls back to the receiver start.
func counterStart(logger *zap.Logger, nic nicReader) time.Time {
	boot, err := nic.bootTime()
	if err != nil {
		logger.Debug("Boot time unknown; counters start at the receiver start", zap.Error(err))
		return time.Now()
	}
	return boot
}

func (r *ethtoolReceiver) Start(ctx context.Context, _ component.Host) error {
	ctx, r.cancel = context.WithCancel(ctx)
	r.wg.Add(1)
	go r.collect(ctx)
	r.logger.Info("ethtool receiver started", zap.Duration("collection_interval", r.interval))
	return nil
}

func (r *ethtoolReceiver) Shutdown(context.Context) error {
	if r.cancel != nil {
		r.cancel()
	}
	r.wg.Wait()
	return r.nic.close()
}

func (r *ethtoolReceiver) collect(ctx context.Context) {
	defer r.wg.Done()
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	r.scrapeAndSend(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.scrapeAndSend(ctx)
		}
	}
}

func (r *ethtoolReceiver) scrapeAndSend(ctx context.Context) {
	md := r.scraper.scrape(time.Now())
	if md.MetricCount() == 0 {
		return
	}
	if err := r.consumer.ConsumeMetrics(ctx, md); err != nil {
		r.logger.Error("Failed to send ethtool metrics", zap.Error(err))
	}
}
