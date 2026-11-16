// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package cloudwatchmetricsreceiver

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"go.opentelemetry.io/collector/extension/xextension/storage"
	"go.uber.org/zap"
)

// stateStore keeps the end of the last completed window per account, region and tier.
type stateStore interface {
	// Get reports ok false when there is no entry or it cannot be read.
	Get(ctx context.Context, key string) (time.Time, bool)
	Set(ctx context.Context, key string, end time.Time) error
}

func stateKey(account, region, tier string) string {
	return fmt.Sprintf("cloudwatchmetrics/%s/%s/%s", account, region, tier)
}

type memoryState struct {
	mu   sync.Mutex
	ends map[string]time.Time
}

func newMemoryState() *memoryState { return &memoryState{ends: map[string]time.Time{}} }

func (m *memoryState) Get(_ context.Context, key string) (time.Time, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.ends[key]
	return t, ok
}

func (m *memoryState) Set(_ context.Context, key string, end time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ends[key] = end
	return nil
}

// storageState writes the end as Unix seconds in text.
type storageState struct {
	client storage.Client
	logger *zap.Logger
}

func newStorageState(client storage.Client, logger *zap.Logger) *storageState {
	return &storageState{client: client, logger: logger}
}

func (s *storageState) Get(ctx context.Context, key string) (time.Time, bool) {
	data, err := s.client.Get(ctx, key)
	if err != nil {
		s.logger.Warn("Cannot read the saved window end, reading the previous complete window only", zap.String("key", key), zap.Error(err))
		return time.Time{}, false
	}
	if len(data) == 0 {
		return time.Time{}, false
	}
	seconds, err := strconv.ParseInt(string(data), 10, 64)
	if err != nil {
		s.logger.Warn("Saved window end is not a number, reading the previous complete window only", zap.String("key", key), zap.Error(err))
		return time.Time{}, false
	}
	return time.Unix(seconds, 0).UTC(), true
}

func (s *storageState) Set(ctx context.Context, key string, end time.Time) error {
	return s.client.Set(ctx, key, []byte(strconv.FormatInt(end.Unix(), 10)))
}
