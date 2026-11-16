// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package cloudwatchmetricsreceiver

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/extension/xextension/storage"
	"go.uber.org/zap"
)

// fakeStorageClient is an in-memory storage.Client.
type fakeStorageClient struct {
	mu     sync.Mutex
	data   map[string][]byte
	getErr error
	setErr error
	closed bool
}

func newFakeStorageClient() *fakeStorageClient { return &fakeStorageClient{data: map[string][]byte{}} }

func (f *fakeStorageClient) Get(_ context.Context, key string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.data[key], nil
}

func (f *fakeStorageClient) Set(_ context.Context, key string, value []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.setErr != nil {
		return f.setErr
	}
	f.data[key] = value
	return nil
}

func (f *fakeStorageClient) Delete(_ context.Context, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.data, key)
	return nil
}

func (f *fakeStorageClient) Batch(context.Context, ...*storage.Operation) error { return nil }

func (f *fakeStorageClient) Close(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}

func (f *fakeStorageClient) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.data)
}

func (f *fakeStorageClient) has(key string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.data[key]
	return ok
}

func (f *fakeStorageClient) isClosed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}

func TestStateKeyNamesAccountRegionAndTier(t *testing.T) {
	assert.Equal(t, "cloudwatchmetrics/prod/us-east-1/daily", stateKey("prod", "us-east-1", "daily"))
}

func TestStorageStateRoundTripsTheWindowEnd(t *testing.T) {
	client := newFakeStorageClient()
	store := newStorageState(client, zap.NewNop())
	key := stateKey("prod", "us-east-1", "daily")

	_, ok := store.Get(context.Background(), key)
	assert.False(t, ok, "no entry yet")

	require.NoError(t, store.Set(context.Background(), key, utc(15, 0, 0)))
	got, ok := store.Get(context.Background(), key)

	assert.True(t, ok)
	assert.True(t, utc(15, 0, 0).Equal(got))
	assert.Equal(t, "1794700800", string(client.data[key]), "Unix seconds as text")
}

func TestStorageStateUnreadableOrGarbledEntryCountsAsNoState(t *testing.T) {
	client := newFakeStorageClient()
	store := newStorageState(client, zap.NewNop())
	key := stateKey("prod", "us-east-1", "daily")

	client.getErr = errors.New("disk")
	_, ok := store.Get(context.Background(), key)
	assert.False(t, ok, "a read error is no state")

	client.getErr = nil
	client.data[key] = []byte("not a number")
	_, ok = store.Get(context.Background(), key)
	assert.False(t, ok, "a garbled entry is no state")
}

func TestStorageStateSetErrorIsReturned(t *testing.T) {
	client := newFakeStorageClient()
	client.setErr = errors.New("full")
	store := newStorageState(client, zap.NewNop())

	assert.ErrorContains(t, store.Set(context.Background(), "k", utc(15, 0, 0)), "full")
}

func TestMemoryStateKeepsTheLastWindowEnd(t *testing.T) {
	store := newMemoryState()

	_, ok := store.Get(context.Background(), "k")
	assert.False(t, ok)

	require.NoError(t, store.Set(context.Background(), "k", utc(15, 0, 0)))
	got, ok := store.Get(context.Background(), "k")

	assert.True(t, ok)
	assert.True(t, utc(15, 0, 0).Equal(got))
}
