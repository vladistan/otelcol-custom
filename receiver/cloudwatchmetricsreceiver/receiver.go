// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package cloudwatchmetricsreceiver

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sync"
	"time"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/extension/xextension/storage"
	"go.opentelemetry.io/collector/receiver"
	"go.uber.org/zap"
)

// pairState is one account, region and tier, polled by its own goroutine.
type pairState struct {
	account  AccountConfig
	region   string
	tierName string
	tier     TierConfig

	api          metricsAPI
	clientWarned bool
	truncated    bool
}

type cwReceiver struct {
	settings receiver.Settings
	logger   *zap.Logger
	cfg      *Config
	consumer consumer.Metrics
	version  string

	clients    clientFactory
	store      stateStore
	closeStore func(context.Context) error
	now        func() time.Time
	telemetry  *selfMetrics

	mu             sync.Mutex
	percentileSeen map[string]struct{}

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func newReceiver(settings receiver.Settings, cfg *Config, next consumer.Metrics) *cwReceiver {
	return &cwReceiver{
		settings:       settings,
		logger:         settings.Logger,
		cfg:            cfg,
		consumer:       next,
		version:        settings.BuildInfo.Version,
		clients:        newAWSFactory(),
		now:            time.Now,
		telemetry:      newSelfMetrics(settings.MeterProvider, settings.Logger),
		percentileSeen: map[string]struct{}{},
	}
}

// newPairs lists every account, region and tier, tiers in name order.
func (r *cwReceiver) newPairs() []*pairState {
	var out []*pairState
	for _, acct := range r.cfg.Accounts {
		for _, region := range acct.Regions {
			for _, name := range slices.Sorted(maps.Keys(r.cfg.Tiers)) {
				out = append(out, &pairState{account: acct, region: region, tierName: name, tier: r.cfg.Tiers[name]})
			}
		}
	}
	return out
}

func (r *cwReceiver) Start(ctx context.Context, host component.Host) error {
	if err := r.openStore(ctx, host); err != nil {
		return err
	}
	ctx, r.cancel = context.WithCancel(context.WithoutCancel(ctx))
	for _, p := range r.newPairs() {
		r.wg.Add(1)
		go r.run(ctx, p)
	}
	r.logger.Info("CloudWatch metrics receiver started", zap.Int("accounts", len(r.cfg.Accounts)), zap.Int("tiers", len(r.cfg.Tiers)))
	return nil
}

func (r *cwReceiver) openStore(ctx context.Context, host component.Host) error {
	if r.cfg.StorageID == nil {
		if r.store == nil {
			r.store = newMemoryState()
		}
		return nil
	}
	ext, ok := host.GetExtensions()[*r.cfg.StorageID]
	if !ok {
		return fmt.Errorf("storage extension %q not found", r.cfg.StorageID.String())
	}
	se, ok := ext.(storage.Extension)
	if !ok {
		return fmt.Errorf("extension %q is not a storage extension", r.cfg.StorageID.String())
	}
	client, err := se.GetClient(ctx, component.KindReceiver, r.settings.ID, "")
	if err != nil {
		return fmt.Errorf("storage client: %w", err)
	}
	r.store = newStorageState(client, r.logger)
	r.closeStore = client.Close
	return nil
}

func (r *cwReceiver) Shutdown(ctx context.Context) error {
	if r.cancel != nil {
		r.cancel()
	}
	r.wg.Wait()
	if r.closeStore != nil {
		return r.closeStore(ctx)
	}
	return nil
}

func (r *cwReceiver) run(ctx context.Context, p *pairState) {
	defer r.wg.Done()
	ticker := time.NewTicker(p.tier.CollectionInterval)
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

func (r *cwReceiver) pairFields(p *pairState) []zap.Field {
	return []zap.Field{zap.String("account", p.account.Name), zap.String("region", p.region), zap.String("tier", p.tierName)}
}

// poll reads every window that is due for one pair. A window is saved only after
// its metrics were delivered, so a failure repeats it at the next poll.
func (r *cwReceiver) poll(ctx context.Context, p *pairState) {
	if !r.ensureClient(ctx, p) {
		return
	}
	key := stateKey(p.account.Name, p.region, p.tierName)
	end := windowEnd(r.now(), p.tier.Delay, p.tier.Period)
	last, have := r.store.Get(ctx, key)
	due := missedWindows(end, p.tier.CollectionInterval, last, have, r.cfg.MaxCatchup)
	if len(due) == 0 {
		return
	}
	found, err := discoverSeries(ctx, p.api, p.tier.Namespaces, p.tier.Limit)
	if err != nil {
		r.logger.Warn("CloudWatch series discovery failed", append(r.pairFields(p), zap.Error(err))...)
		return
	}
	r.reportDiscovery(ctx, p, found)
	for _, w := range due {
		if !r.readAndSend(ctx, p, found.series, w) {
			return
		}
		if err := r.store.Set(ctx, key, w.end); err != nil {
			r.logger.Warn("Cannot save the window end", append(r.pairFields(p), zap.Error(err))...)
			return
		}
	}
}

// ensureClient builds the pair's client once; a failure, such as a refused role,
// is logged once and tried again at the next poll.
func (r *cwReceiver) ensureClient(ctx context.Context, p *pairState) bool {
	if p.api != nil {
		return true
	}
	api, err := r.clients.client(ctx, p.account, p.region)
	if err != nil {
		if !p.clientWarned {
			p.clientWarned = true
			r.logger.Warn("Cannot create the CloudWatch client for this account and region", append(r.pairFields(p), zap.Error(err))...)
		}
		return false
	}
	p.api, p.clientWarned = api, false
	return true
}

func (r *cwReceiver) reportDiscovery(ctx context.Context, p *pairState, found discovered) {
	r.telemetry.record(ctx, p, found)
	if found.truncated() && !p.truncated {
		r.logger.Warn("Series discovery is over the limit; the rest are not read",
			append(r.pairFields(p), zap.Int("discovered", found.total), zap.Int("limit", p.tier.Limit))...)
	}
	p.truncated = found.truncated()
}

func (r *cwReceiver) readAndSend(ctx context.Context, p *pairState, series []seriesRef, w window) bool {
	vals, err := readWindow(ctx, p.api, series, p.tier.Stats, p.tier.Period, w)
	if err != nil {
		r.logger.Warn("CloudWatch read failed", append(r.pairFields(p), zap.Error(err))...)
		return false
	}
	for _, name := range missingPercentiles(series, vals, p.tier.Stats) {
		r.notePercentileGap(name)
	}
	md := buildMetrics(r.version, p.account.Name, p.region, series, vals, p.tier.Stats)
	if md.ResourceMetrics().Len() == 0 {
		return true
	}
	if err := r.consumer.ConsumeMetrics(ctx, md); err != nil {
		r.logger.Error("Failed to send CloudWatch metrics", append(r.pairFields(p), zap.Error(err))...)
		return false
	}
	return true
}

// notePercentileGap logs the first time a metric returned no percentile data in this run.
func (r *cwReceiver) notePercentileGap(name string) {
	r.mu.Lock()
	_, seen := r.percentileSeen[name]
	r.percentileSeen[name] = struct{}{}
	r.mu.Unlock()
	if !seen {
		r.logger.Info("Metric has no percentile statistic, so no percentile point is emitted for it", zap.String("metric", name))
	}
}
