// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package zfskstatreceiver

import (
	"context"
	"sync"
	"time"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/receiver"
	"go.uber.org/zap"
)

// zfsReceiver scrapes the kstat files at a fixed interval.
type zfsReceiver struct {
	logger   *zap.Logger
	config   *Config
	consumer consumer.Metrics
	scraper  *scraper

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func newReceiver(settings receiver.Settings, cfg *Config, next consumer.Metrics) *zfsReceiver {
	return &zfsReceiver{
		logger:   settings.Logger,
		config:   cfg,
		consumer: next,
		scraper:  newScraper(settings.Logger, cfg),
	}
}

func (r *zfsReceiver) Start(ctx context.Context, _ component.Host) error {
	ctx, r.cancel = context.WithCancel(ctx)
	r.wg.Add(1)
	go r.collect(ctx)
	r.logger.Info("ZFS kstat receiver started",
		zap.Duration("collection_interval", r.config.CollectionInterval),
		zap.String("source", r.config.resolvedSource()),
		zap.String("kstat_path", r.config.KstatPath))
	return nil
}

func (r *zfsReceiver) Shutdown(context.Context) error {
	if r.cancel != nil {
		r.cancel()
	}
	r.wg.Wait()
	return nil
}

func (r *zfsReceiver) collect(ctx context.Context) {
	defer r.wg.Done()
	ticker := time.NewTicker(r.config.CollectionInterval)
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

func (r *zfsReceiver) scrapeAndSend(ctx context.Context) {
	md, err := r.scraper.scrape(ctx)
	if err != nil {
		r.logger.Error("Failed to scrape ZFS kstat metrics", zap.Error(err))
		return
	}
	if md.ResourceMetrics().Len() == 0 {
		return
	}
	if err := r.consumer.ConsumeMetrics(ctx, md); err != nil {
		r.logger.Error("Failed to send ZFS kstat metrics", zap.Error(err))
	}
}
