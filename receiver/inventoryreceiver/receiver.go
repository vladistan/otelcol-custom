// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package inventoryreceiver

import (
	"context"
	"sync"
	"time"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/receiver"
	"go.uber.org/zap"
)

type inventoryReceiver struct {
	logger   *zap.Logger
	interval time.Duration
	consumer consumer.Metrics
	src      source

	// newTicker and now are replaced by tests.
	newTicker func(time.Duration) (<-chan time.Time, func())
	now       func() time.Time

	cancel context.CancelFunc
	wg     sync.WaitGroup

	// Log state, touched only by the collection goroutine.
	loggedHost, loggedDMI, loggedCap, sendFailed bool
}

func newReceiver(settings receiver.Settings, cfg *Config, next consumer.Metrics, src source) *inventoryReceiver {
	return &inventoryReceiver{
		logger:   settings.Logger,
		interval: cfg.CollectionInterval,
		consumer: next,
		src:      src,
		now:      time.Now,
		newTicker: func(d time.Duration) (<-chan time.Time, func()) {
			t := time.NewTicker(d)
			return t.C, t.Stop
		},
	}
}

func (r *inventoryReceiver) Start(ctx context.Context, _ component.Host) error {
	ctx, r.cancel = context.WithCancel(context.WithoutCancel(ctx))
	r.wg.Add(1)
	go r.loop(ctx)
	r.logger.Info("inventory receiver started", zap.Duration("collection_interval", r.interval))
	return nil
}

func (r *inventoryReceiver) Shutdown(context.Context) error {
	if r.cancel != nil {
		r.cancel()
	}
	r.wg.Wait()
	return nil
}

func (r *inventoryReceiver) loop(ctx context.Context) {
	defer r.wg.Done()
	ticks, stop := r.newTicker(r.interval)
	defer stop()

	r.collect(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticks:
			r.collect(ctx)
		}
	}
}

// collect runs one read. The read is a goroutine of its own, so a file that
// never returns cannot hold Shutdown; the loop does not start another read
// until this one has returned.
func (r *inventoryReceiver) collect(ctx context.Context) {
	done := make(chan inventory, 1)
	go func() { done <- r.src.read(ctx) }()
	select {
	case <-ctx.Done():
	case inv := <-done:
		r.send(ctx, inv)
	}
}

func (r *inventoryReceiver) send(ctx context.Context, inv inventory) {
	switch inv.host {
	case hostVirtual, hostContainer:
		if !r.loggedHost {
			r.loggedHost = true
			what := "virtual"
			if inv.host == hostContainer {
				what = "a container"
			}
			r.logger.Info("Host is "+what+"; no inventory is sent", zap.String("reason", inv.hostReason))
		}
		return
	case hostUndecided:
		if !r.loggedHost {
			r.loggedHost = true
			r.logger.Info("Host type cannot be decided; it is treated as physical", zap.String("reason", inv.hostReason))
		}
	}
	if inv.dmiUnreadable && !r.loggedDMI {
		r.loggedDMI = true
		r.logger.Info("DMI tables are unreadable; memory, board, BIOS and system facts may be left out (read access to the tables is needed)")
	}
	if inv.drivesCapped && !r.loggedCap {
		r.loggedCap = true
		r.logger.Info("More drives than max_drives; the rest are left out")
	}

	for _, note := range inv.notes {
		r.logger.Debug("Inventory fact left out", zap.String("fact", note))
	}

	md := buildMetrics(inv, r.now())
	if md.MetricCount() == 0 {
		return
	}
	if err := r.consumer.ConsumeMetrics(ctx, md); err != nil {
		if !r.sendFailed {
			r.sendFailed = true
			r.logger.Error("Failed to send inventory metrics", zap.Error(err))
		}
		return
	}
	r.sendFailed = false
}
