// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package megaraidreceiver

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const eventReadFailed = "Cannot read the controller event log; new events are not read until it works"

func eventErrors(t *testing.T, s *scraper) float64 {
	t.Helper()
	p, ok := find(s.scrape(context.Background()).metrics, metricEventErrors, "hw.id", testCtrl.id())
	require.True(t, ok, "the failure counter is always sent while events are on")
	return p.val
}

func TestAStorageReadErrorSkipsTheEventsAndIsNotAFirstRun(t *testing.T) {
	dev := labFake(t)
	eventLog(dev, 50, allInfo)
	store := newMemStore()
	store.data[cursorKey(testCtrl)] = []byte("10")
	store.failGet = errors.New("disk full")
	s, logs, _ := newTestScraper(t, createDefaultConfig().(*Config), dev)
	s.events = &eventCursor{store: store}

	out := s.scrape(context.Background())
	again := s.scrape(context.Background())

	assert.Zero(t, out.logs.LogRecordCount())
	assert.Empty(t, out.commit, "no position is saved after a failed read")
	assert.Empty(t, again.commit)
	assert.Zero(t, dev.count(opEventGet), "the backlog is not read as a first run")
	assert.Equal(t, uint32(10), store.seq(testCtrl), "the saved position is untouched")
	assert.Equal(t, 1, logs.FilterMessage(eventReadFailed).Len(), "one line for the run of failures")
	assert.Equal(t, 2.0, counter(t, again))

	store.failGet = nil
	back := s.scrape(context.Background())
	assert.Positive(t, back.logs.LogRecordCount(), "the backlog is read once the store works")
	store.failGet = errors.New("disk full")
	s.scrape(context.Background())
	assert.Equal(t, 2, logs.FilterMessage(eventReadFailed).Len(), "a new run of failures logs again")
}

func counter(t *testing.T, out output) float64 {
	t.Helper()
	p, ok := find(out.metrics, metricEventErrors, "hw.id", testCtrl.id())
	require.True(t, ok)
	return p.val
}

func TestAFailedEventCommandIsLoggedOnceAndCounted(t *testing.T) {
	dev := labFake(t)
	eventLog(dev, 50, allInfo)
	dev.dynamic[opEventGet] = func(request) (response, error) { return response{}, errControllerBusy }
	store := newMemStore()
	store.data[cursorKey(testCtrl)] = []byte("10")
	s, logs, _ := newTestScraper(t, createDefaultConfig().(*Config), dev)
	s.events = &eventCursor{store: store}

	first := counter(t, s.scrape(context.Background()))
	second := counter(t, s.scrape(context.Background()))

	assert.Equal(t, 1.0, first)
	assert.Equal(t, 2.0, second)
	assert.Equal(t, 1, logs.FilterMessage(eventReadFailed).Len())
	assert.Equal(t, uint32(10), store.seq(testCtrl))
}

func TestARefusedEventStateCommandIsCounted(t *testing.T) {
	dev := labFake(t)
	dev.setStatus(opEventGetInfo, 0x33)
	s := eventScraper(t, dev, newMemStore())

	assert.Equal(t, 1.0, eventErrors(t, s))
}

func TestACorruptSavedPositionRestartsAtTheNewestEvent(t *testing.T) {
	dev := labFake(t)
	eventLog(dev, 50, allInfo)
	store := newMemStore()
	store.data[cursorKey(testCtrl)] = []byte("not a number")
	s, logs, _ := newTestScraper(t, createDefaultConfig().(*Config), dev)
	s.events = &eventCursor{store: store}

	out := s.scrape(context.Background())
	commitAll(t, out)

	assert.Zero(t, out.logs.LogRecordCount())
	assert.Equal(t, uint32(50), store.seq(testCtrl))
	assert.Equal(t, 1, logs.FilterMessage("The saved event position is not a number; the log restarts at the newest event").Len())
}

func TestHealthyEventReadsReportAZeroCounter(t *testing.T) {
	dev := labFake(t)
	eventLog(dev, 12, allInfo)
	store := newMemStore()
	store.data[cursorKey(testCtrl)] = []byte("10")
	s := eventScraper(t, dev, store)

	assert.Zero(t, eventErrors(t, s))
}
