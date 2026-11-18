// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package pvereceiver

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"sync"
	"time"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/receiver"
	"go.uber.org/zap"
)

// pveReceiver runs the enabled scrapers. One instance serves both the metrics and the logs pipeline.
type pveReceiver struct {
	logger *zap.Logger
	cfg    *Config

	resources *resourcesScraper
	guests    *guestsScraper
	clients   []*apiClient
	// nodeSource says where the guests node came from: "guests.node" or "host name".
	nodeSource string

	mu      sync.Mutex
	metrics consumer.Metrics
	logs    consumer.Logs
	started int
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	rnd     *rand.Rand
}

func newReceiver(settings receiver.Settings, cfg *Config) (*pveReceiver, error) {
	r := &pveReceiver{
		logger: settings.Logger,
		cfg:    cfg,
		rnd:    rand.New(rand.NewSource(time.Now().UnixNano())), //nolint:gosec // a start offset, not a secret
	}
	if cfg.Resources.Enabled {
		client, err := newAPIClient(cfg, cfg.Resources.TokenID, cfg.Resources.TokenSecret)
		if err != nil {
			return nil, err
		}
		r.clients = append(r.clients, client)
		r.resources = newResourcesScraper(settings.Logger, cfg, client, time.Now())
	}
	if cfg.Guests.Enabled {
		client, err := newAPIClient(cfg, cfg.Guests.TokenID, cfg.Guests.TokenSecret)
		if err != nil {
			return nil, err
		}
		node := cfg.Guests.Node
		r.nodeSource = "guests.node"
		if node == "" {
			var err error
			if node, err = localNode(); err != nil {
				return nil, fmt.Errorf("guests.node is empty and the host name is unknown; set guests.node: %w", err)
			}
			r.nodeSource = "host name"
		}
		r.clients = append(r.clients, client)
		r.guests = newGuestsScraper(settings.Logger, cfg, client, node)
	}
	return r, nil
}

func (r *pveReceiver) setMetrics(next consumer.Metrics) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.metrics = next
}

func (r *pveReceiver) setLogs(next consumer.Logs) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.logs = next
}

// Start starts the scrapers on the first call. It makes no network call: each scraper waits for its start
// offset before its first run.
func (r *pveReceiver) Start(context.Context, component.Host) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.started++
	if r.started > 1 {
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	if r.resources != nil {
		r.run(ctx, r.cfg.Resources.CollectionInterval, r.scrapeResourcesOnce)
	}
	if r.guests != nil {
		r.run(ctx, r.cfg.Guests.CollectionInterval, r.sweepGuestsOnce)
	}
	if r.guests != nil {
		msg := "guests node"
		if r.nodeSource == "host name" {
			msg = "guests.node is empty: the guests node is the short host name; in a pod that is the pod name, so set guests.node"
		}
		r.logger.Info(msg, zap.String("node", r.guests.node), zap.String("source", r.nodeSource))
	}
	if r.cfg.InsecureSkipVerify {
		r.logger.Warn("insecure_skip_verify is on: the PVE certificate is not verified")
	}
	r.logger.Info("pve receiver started",
		zap.Bool("resources", r.resources != nil), zap.Bool("guests", r.guests != nil))
	return nil
}

// Shutdown stops the scrapers when the last pipeline shuts down.
func (r *pveReceiver) Shutdown(context.Context) error {
	r.mu.Lock()
	r.started--
	last := r.started <= 0
	cancel := r.cancel
	r.mu.Unlock()
	if !last {
		return nil
	}
	if cancel != nil {
		cancel()
	}
	r.wg.Wait()
	for _, c := range r.clients {
		c.close()
	}
	forget(r.cfg)
	return nil
}

func (r *pveReceiver) run(ctx context.Context, interval time.Duration, fn func(context.Context)) {
	first := startOffset(interval, r.rnd)
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		timer := time.NewTimer(first)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
				fn(ctx)
				timer.Reset(interval)
			}
		}
	}()
}

func (r *pveReceiver) consumers() (consumer.Metrics, consumer.Logs) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.metrics, r.logs
}

// scrapeResourcesOnce runs one interval of the resources scraper.
func (r *pveReceiver) scrapeResourcesOnce(ctx context.Context) {
	md, err := r.resources.scrape(ctx, time.Now())
	metrics, _ := r.consumers()
	if metrics != nil && md.MetricCount() > 0 {
		if cerr := metrics.ConsumeMetrics(ctx, md); cerr != nil {
			r.logger.Error("Failed to send PVE metrics", zap.String("scraper", "resources"), zap.Error(cerr))
		}
	}
	r.logScrapeError("resources", err)
}

// sweepGuestsOnce runs one interval of the guests scraper.
func (r *pveReceiver) sweepGuestsOnce(ctx context.Context) {
	md, lg, err := r.guests.sweep(ctx, time.Now())
	metrics, logs := r.consumers()
	if metrics != nil && md.MetricCount() > 0 {
		if cerr := metrics.ConsumeMetrics(ctx, md); cerr != nil {
			r.logger.Error("Failed to send PVE metrics", zap.String("scraper", "guests"), zap.Error(cerr))
		}
	}
	delivered := lg.LogRecordCount() == 0
	if logs != nil && lg.LogRecordCount() > 0 {
		if cerr := logs.ConsumeLogs(ctx, lg); cerr != nil {
			r.logger.Error("Failed to send PVE logs", zap.Error(cerr))
		} else {
			delivered = true
		}
	}
	// A fact counts as delivered only when the logs consumer took it; otherwise the next sweep finds it again.
	if delivered {
		r.guests.commit()
	} else {
		r.guests.discard()
	}
	r.logScrapeError("guests", err)
}

// logScrapeError writes one line for the failure of one interval: a WARN for a refused token or role, an
// error for anything else. The text never holds the token secret.
func (r *pveReceiver) logScrapeError(scraper string, err error) {
	if err == nil {
		return
	}
	var apiErr *apiError
	if errors.As(err, &apiErr) {
		switch apiErr.Status {
		case http.StatusUnauthorized:
			r.logger.Warn("PVE rejected the token (HTTP 401)", zap.String("scraper", scraper), zap.Error(err))
			return
		case http.StatusForbidden:
			hint := "the resources token needs the PVEAuditor role on /"
			if scraper == "guests" {
				hint = privilegeMessage(apiErr)
			}
			r.logger.Warn("PVE refused the request (HTTP 403)", zap.String("scraper", scraper), zap.String("hint", hint), zap.Error(err))
			return
		}
	}
	r.logger.Error("PVE scrape failed", zap.String("scraper", scraper), zap.Error(err))
}
