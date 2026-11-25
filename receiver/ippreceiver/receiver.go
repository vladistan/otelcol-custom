// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package ippreceiver

import (
	"context"
	"sync"
	"time"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/receiver"
	"go.uber.org/zap"
)

type ippReceiver struct {
	logger   *zap.Logger
	interval time.Duration
	consumer consumer.Metrics
	pollers  []*poller

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func newReceiver(settings receiver.Settings, cfg *Config, next consumer.Metrics) (*ippReceiver, error) {
	pollers := make([]*poller, 0, len(cfg.Printers))
	for _, p := range cfg.Printers {
		pl, err := newPoller(p, cfg.Timeout)
		if err != nil {
			return nil, err
		}
		pollers = append(pollers, pl)
	}
	return &ippReceiver{
		logger:   settings.Logger,
		interval: cfg.CollectionInterval,
		consumer: next,
		pollers:  pollers,
	}, nil
}

// Start runs one polling goroutine per printer, so a slow printer delays no other.
func (r *ippReceiver) Start(_ context.Context, _ component.Host) error {
	// The Start context ends with startup; polling outlives it.
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	for _, p := range r.pollers {
		r.wg.Add(1)
		go r.run(ctx, p)
	}
	r.logger.Info("ipp receiver started",
		zap.Int("printers", len(r.pollers)), zap.Duration("collection_interval", r.interval))
	return nil
}

func (r *ippReceiver) Shutdown(context.Context) error {
	if r.cancel != nil {
		r.cancel()
	}
	r.wg.Wait()
	return nil
}

func (r *ippReceiver) run(ctx context.Context, p *poller) {
	defer r.wg.Done()
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	r.poll(ctx, p)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.poll(ctx, p)
		}
	}
}

// poll reads one printer and sends the series. A failure warns once per episode.
func (r *ippReceiver) poll(ctx context.Context, p *poller) {
	attrs, err := p.fetch(ctx)
	if ctx.Err() != nil {
		return
	}
	now := time.Now()
	var md pmetric.Metrics
	if err != nil {
		if !p.failing {
			r.logger.Warn("Printer poll failed", zap.String("printer", p.name), zap.Error(err))
		}
		p.failing = true
		md = failedMetrics(p.name, now)
	} else {
		if p.failing {
			r.logger.Info("Printer poll succeeded again", zap.String("printer", p.name))
		}
		p.failing = false
		md = printerMetrics(p.name, attrs, now)
	}
	if err := r.consumer.ConsumeMetrics(ctx, md); err != nil {
		r.logger.Error("Failed to send printer metrics", zap.String("printer", p.name), zap.Error(err))
	}
}
