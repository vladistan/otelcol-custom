// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package ksmreceiver

import (
	"context"
	"sync"
	"time"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/receiver"
	"go.uber.org/zap"
)

// ksmReceiver scrapes the KSM files at a fixed interval.
type ksmReceiver struct {
	logger   *zap.Logger
	config   *Config
	consumer consumer.Metrics
	scraper  *scraper

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func newReceiver(settings receiver.Settings, cfg *Config, next consumer.Metrics) *ksmReceiver {
	return &ksmReceiver{
		logger:   settings.Logger,
		config:   cfg,
		consumer: next,
		scraper:  newScraper(settings.Logger, cfg),
	}
}

// Start runs the scrape loop on its own context: the context the host passes to Start may end
// once Start returns. Shutdown stops the loop.
func (r *ksmReceiver) Start(_ context.Context, _ component.Host) error {
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	r.wg.Add(1)
	go r.collect(ctx)
	r.logger.Info("KSM receiver started",
		zap.Duration("collection_interval", r.config.CollectionInterval),
		zap.String("ksm_path", r.config.KsmPath))
	return nil
}

func (r *ksmReceiver) Shutdown(context.Context) error {
	if r.cancel != nil {
		r.cancel()
	}
	r.wg.Wait()
	return nil
}

func (r *ksmReceiver) collect(ctx context.Context) {
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

func (r *ksmReceiver) scrapeAndSend(ctx context.Context) {
	md, err := r.scraper.scrape(ctx)
	if err != nil {
		if ctx.Err() == nil {
			r.logger.Error("Failed to scrape KSM metrics", zap.Error(err))
		}
		return
	}
	if md.ResourceMetrics().Len() == 0 {
		return
	}
	// A stop during the scrape must not drop the batch that was read.
	if err := r.consumer.ConsumeMetrics(context.WithoutCancel(ctx), md); err != nil {
		r.logger.Error("Failed to send KSM metrics", zap.Error(err))
	}
}
