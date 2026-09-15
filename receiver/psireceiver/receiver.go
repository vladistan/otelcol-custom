package psireceiver

import (
	"context"
	"sync"
	"time"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/receiver"
	"go.uber.org/zap"
)

// psiReceiver collects PSI metrics at regular intervals.
type psiReceiver struct {
	logger   *zap.Logger
	config   *Config
	consumer consumer.Metrics
	scraper  *psiScraper

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func newPSIReceiver(
	settings receiver.Settings,
	cfg *Config,
	consumer consumer.Metrics,
) (*psiReceiver, error) {
	scraper, err := newPSIScraper(settings.Logger, cfg)
	if err != nil {
		return nil, err
	}

	return &psiReceiver{
		logger:   settings.Logger,
		config:   cfg,
		consumer: consumer,
		scraper:  scraper,
	}, nil
}

func (r *psiReceiver) Start(ctx context.Context, host component.Host) error {
	ctx, r.cancel = context.WithCancel(ctx)

	r.wg.Add(1)
	go r.collect(ctx)

	r.logger.Info("PSI receiver started",
		zap.Duration("collection_interval", r.config.CollectionInterval),
		zap.String("proc_path", r.config.ProcPath))

	return nil
}

func (r *psiReceiver) Shutdown(ctx context.Context) error {
	if r.cancel != nil {
		r.cancel()
	}
	r.wg.Wait()
	r.logger.Info("PSI receiver stopped")
	return nil
}

func (r *psiReceiver) collect(ctx context.Context) {
	defer r.wg.Done()

	ticker := time.NewTicker(r.config.CollectionInterval)
	defer ticker.Stop()

	r.scrapeAndSend(ctx) // collect immediately, then on ticker

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.scrapeAndSend(ctx)
		}
	}
}

func (r *psiReceiver) scrapeAndSend(ctx context.Context) {
	metrics, err := r.scraper.scrape(ctx)
	if err != nil {
		r.logger.Error("Failed to scrape PSI metrics", zap.Error(err))
		return
	}

	if metrics.ResourceMetrics().Len() == 0 {
		return
	}

	if err := r.consumer.ConsumeMetrics(ctx, metrics); err != nil {
		r.logger.Error("Failed to send PSI metrics", zap.Error(err))
	}
}
