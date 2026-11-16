// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package hwmonreceiver

import (
	"context"
	"sync"
	"time"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/receiver"
	"go.uber.org/zap"
)

type hwmonReceiver struct {
	logger   *zap.Logger
	interval time.Duration
	consumer consumer.Metrics
	scraper  *scraper

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func newReceiver(settings receiver.Settings, cfg *Config, next consumer.Metrics, src source) *hwmonReceiver {
	return &hwmonReceiver{
		logger:   settings.Logger,
		interval: cfg.CollectionInterval,
		consumer: next,
		scraper:  newScraper(settings.Logger, cfg, src),
	}
}

func (r *hwmonReceiver) Start(ctx context.Context, _ component.Host) error {
	ctx, r.cancel = context.WithCancel(ctx)
	r.wg.Add(1)
	go r.collect(ctx)
	r.logger.Info("hwmon receiver started", zap.Duration("collection_interval", r.interval))
	return nil
}

func (r *hwmonReceiver) Shutdown(context.Context) error {
	if r.cancel != nil {
		r.cancel()
	}
	r.wg.Wait()
	return nil
}

func (r *hwmonReceiver) collect(ctx context.Context) {
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

func (r *hwmonReceiver) scrapeAndSend(ctx context.Context) {
	md := r.scraper.scrape(time.Now())
	if md.MetricCount() == 0 {
		return
	}
	if err := r.consumer.ConsumeMetrics(ctx, md); err != nil {
		r.logger.Error("Failed to send hwmon metrics", zap.Error(err))
	}
}
