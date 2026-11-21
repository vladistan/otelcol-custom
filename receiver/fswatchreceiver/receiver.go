// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package fswatchreceiver

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/extension/xextension/storage"
	"go.opentelemetry.io/collector/receiver"
)

type fswatchReceiver struct {
	settings receiver.Settings
	cfg      *Config
	next     consumer.Logs

	checkPlatform func() error
	tree          tree
	open          openFunc
	backoffMin    time.Duration
	backoffMax    time.Duration

	cancel context.CancelFunc
	wg     sync.WaitGroup
	client storage.Client
}

func newReceiver(settings receiver.Settings, cfg *Config, next consumer.Logs) (*fswatchReceiver, error) {
	return &fswatchReceiver{
		settings:      settings,
		cfg:           cfg,
		next:          next,
		checkPlatform: platformCheck,
		tree:          osTree{},
		open:          openWatcher,
		backoffMin:    5 * time.Second,
		backoffMax:    time.Minute,
	}, nil
}

func (r *fswatchReceiver) Start(ctx context.Context, host component.Host) error {
	if err := r.checkPlatform(); err != nil {
		return err
	}
	tel, err := newTelemetry(r.settings.TelemetrySettings)
	if err != nil {
		return err
	}
	specs := r.cfg.rootSpecs()
	matchers := make([]*matcher, len(specs))
	for i, spec := range specs {
		m, err := newMatcher(spec.patterns())
		if err != nil {
			return err
		}
		matchers[i] = m
	}
	var store stateStore
	if r.cfg.StorageID != nil {
		client, err := r.openStorageClient(ctx, host, *r.cfg.StorageID)
		if err != nil {
			return err
		}
		r.client, store = client, client
	}

	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	r.cancel = cancel
	for i, spec := range specs {
		rw := newRootWatch(rootParams{
			cfg: r.cfg, root: spec.path, name: spec.name, logger: r.settings.Logger, next: r.next, tel: tel,
			tree: r.tree, store: store, open: r.open, now: time.Now, match: matchers[i],
		})
		rw.backoffMin, rw.backoffMax = r.backoffMin, r.backoffMax
		r.wg.Add(1)
		go func() {
			defer r.wg.Done()
			rw.run(runCtx)
		}()
	}
	return nil
}

func (r *fswatchReceiver) openStorageClient(ctx context.Context, host component.Host, id component.ID) (storage.Client, error) {
	ext, ok := host.GetExtensions()[id]
	if !ok {
		return nil, fmt.Errorf("storage extension %q not found", id)
	}
	se, ok := ext.(storage.Extension)
	if !ok {
		return nil, fmt.Errorf("extension %q is not a storage extension", id)
	}
	return se.GetClient(ctx, component.KindReceiver, r.settings.ID, "")
}

func (r *fswatchReceiver) Shutdown(ctx context.Context) error {
	if r.cancel != nil {
		r.cancel()
	}
	stopped := make(chan struct{})
	go func() {
		r.wg.Wait()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-ctx.Done():
		return fmt.Errorf("root watchers did not stop: %w", ctx.Err())
	}
	if r.client != nil {
		return r.client.Close(ctx)
	}
	return nil
}
