// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package iscsireceiver

import (
	"context"
	"sync"
	"time"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/receiver"
	"go.uber.org/zap"
)

type iscsiReceiver struct {
	logger   *zap.Logger
	interval time.Duration
	consumer consumer.Metrics
	scraper  *scraper

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func newReceiver(settings receiver.Settings, cfg *Config, next consumer.Metrics, src sessionSource) *iscsiReceiver {
	sc := newScraper(settings.Logger, src)
	if ls, ok := src.(lunSource); ok && cfg.LUNs {
		sc.luns, sc.maxLUNs = ls, cfg.MaxLUNs
	}
	return &iscsiReceiver{
		logger:   settings.Logger,
		interval: cfg.CollectionInterval,
		consumer: next,
		scraper:  sc,
	}
}

func (r *iscsiReceiver) Start(ctx context.Context, _ component.Host) error {
	ctx, r.cancel = context.WithCancel(ctx)
	r.wg.Add(1)
	go r.collect(ctx)
	r.logger.Info("iscsi receiver started", zap.Duration("collection_interval", r.interval))
	return nil
}

func (r *iscsiReceiver) Shutdown(context.Context) error {
	if r.cancel != nil {
		r.cancel()
	}
	r.wg.Wait()
	return nil
}

func (r *iscsiReceiver) collect(ctx context.Context) {
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

func (r *iscsiReceiver) scrapeAndSend(ctx context.Context) {
	md := r.scraper.scrape(time.Now())
	if md.MetricCount() == 0 {
		return
	}
	if err := r.consumer.ConsumeMetrics(ctx, md); err != nil {
		r.logger.Error("Failed to send iscsi metrics", zap.Error(err))
	}
}
