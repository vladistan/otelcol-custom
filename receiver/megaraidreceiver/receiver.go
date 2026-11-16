// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package megaraidreceiver

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/extension/xextension/storage"
	"go.opentelemetry.io/collector/receiver"
	"go.uber.org/zap"
)

// megaReceiver is the metrics and the logs receiver of one configuration. Start and Shutdown run
// once for each pipeline; the read loop runs while at least one of them is started.
type megaReceiver struct {
	settings receiver.Settings
	cfg      *Config
	logger   *zap.Logger
	metrics  consumer.Metrics
	logs     consumer.Logs

	firstDelay time.Duration
	scraper    *scraper
	closeStore func(context.Context) error

	mu         sync.Mutex
	started    int
	cancel     context.CancelFunc
	wg         sync.WaitGroup
	storeWarn  bool
	storeError string
}

func newReceiver(settings receiver.Settings, cfg *Config) *megaReceiver {
	r := &megaReceiver{settings: settings, cfg: cfg, logger: settings.Logger}
	r.scraper = newScraper(settings.Logger, cfg,
		func() ([]controller, error) { return findControllers(sysRoot) },
		func(hostNo int) (device, func(), error) { return openNode(hostNo, cfg.CreateNode) }, time.Now)
	return r
}

// Start opens the event storage and begins the read loop on the first call. It sends nothing to
// any controller.
func (r *megaReceiver) Start(ctx context.Context, host component.Host) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.started++
	if r.started > 1 {
		return nil
	}
	if r.cfg.Events.Enabled && r.logs != nil {
		if err := r.openStore(ctx, host); err != nil {
			r.storeError = err.Error()
		}
	}
	loopCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	r.cancel = cancel
	r.wg.Add(1)
	go r.collect(loopCtx)
	r.logger.Info("megaraid receiver started", zap.Duration("collection_interval", r.cfg.CollectionInterval), zap.Bool("smart", r.cfg.Smart))
	return nil
}

func (r *megaReceiver) openStore(ctx context.Context, host component.Host) error {
	id := r.cfg.Events.Storage
	if id == nil {
		return fmt.Errorf("events.enabled needs events.storage to name a file_storage extension")
	}
	ext, ok := host.GetExtensions()[*id]
	if !ok {
		return fmt.Errorf("the file_storage extension %q named by events.storage is not configured", id.String())
	}
	se, ok := ext.(storage.Extension)
	if !ok {
		return fmt.Errorf("extension %q is not a file_storage extension", id.String())
	}
	client, err := se.GetClient(ctx, component.KindReceiver, r.settings.ID, "")
	if err != nil {
		return fmt.Errorf("file_storage client: %w", err)
	}
	r.scraper.events = &eventCursor{store: client}
	r.closeStore = client.Close
	return nil
}

// Shutdown stops the loop with the last pipeline and waits for a read in progress.
func (r *megaReceiver) Shutdown(ctx context.Context) error {
	r.mu.Lock()
	if r.started == 0 {
		r.mu.Unlock()
		return nil
	}
	r.started--
	if r.started > 0 {
		r.mu.Unlock()
		return nil
	}
	cancel := r.cancel
	r.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	r.wg.Wait()
	if r.closeStore != nil {
		return r.closeStore(ctx)
	}
	return nil
}

func (r *megaReceiver) collect(ctx context.Context) {
	defer r.wg.Done()
	first := time.NewTimer(r.firstDelay)
	defer first.Stop()
	select {
	case <-ctx.Done():
		return
	case <-first.C:
	}
	r.scrapeAndSend(ctx)
	ticker := time.NewTicker(r.cfg.CollectionInterval)
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

// scrapeAndSend reads the controllers and sends the metrics and the events. A missing file_storage
// is one error per start; metrics go on.
func (r *megaReceiver) scrapeAndSend(ctx context.Context) {
	if r.storeError != "" && !r.storeWarn {
		r.storeWarn = true
		r.logger.Error("Controller events are off: "+r.storeError+"; the receiver needs the file_storage extension to keep its place in the event log", zap.String("extension", "file_storage"))
	}
	out := r.scraper.scrape(ctx)
	if r.metrics != nil && out.metrics.MetricCount() > 0 {
		if err := r.metrics.ConsumeMetrics(ctx, out.metrics); err != nil {
			r.logger.Error("Failed to send megaraid metrics", zap.Error(err))
		}
	}
	delivered := true
	if r.logs != nil && out.logs.LogRecordCount() > 0 {
		if err := r.logs.ConsumeLogs(ctx, out.logs); err != nil {
			delivered = false
			r.logger.Error("Failed to send megaraid events; they are read again", zap.Error(err))
		}
	}
	if !delivered {
		return
	}
	for _, commit := range out.commit {
		if err := commit(ctx); err != nil {
			r.logger.Error("Failed to save the event position", zap.Error(err))
		}
	}
}
