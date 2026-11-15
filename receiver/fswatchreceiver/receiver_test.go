// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package fswatchreceiver

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/component/componenttest"
	"go.opentelemetry.io/collector/extension/xextension/storage"
	"go.opentelemetry.io/collector/receiver/receivertest"
)

type fakeWatcher struct {
	msgs   chan watchMsg
	closed atomic.Bool
}

func newFakeWatcher() *fakeWatcher { return &fakeWatcher{msgs: make(chan watchMsg, 16)} }

func (w *fakeWatcher) Messages() <-chan watchMsg { return w.msgs }

func (w *fakeWatcher) Close() error {
	if w.closed.CompareAndSwap(false, true) {
		close(w.msgs)
	}
	return nil
}

type storageClient struct {
	*memStore
	closed atomic.Bool
}

func (c *storageClient) Delete(context.Context, string) error               { return nil }
func (c *storageClient) Batch(context.Context, ...*storage.Operation) error { return nil }
func (c *storageClient) Close(context.Context) error                        { c.closed.Store(true); return nil }

type storageExtension struct {
	component.Component
	client *storageClient
}

func (e *storageExtension) GetClient(context.Context, component.Kind, component.ID, string) (storage.Client, error) {
	return e.client, nil
}

type plainExtension struct{ component.Component }

type extHost struct {
	component.Host
	exts map[component.ID]component.Component
}

func (h extHost) GetExtensions() map[component.ID]component.Component { return h.exts }

func newTestReceiver(t *testing.T, tree tree, open openFunc) (*fswatchReceiver, *flakySink) {
	t.Helper()
	cfg := createDefaultConfig().(*Config)
	cfg.Paths = []string{testRoot}
	sink := &flakySink{}
	rcv, err := newReceiver(receivertest.NewNopSettings(component.MustNewType(typeStr)), cfg, sink)
	if err != nil {
		t.Fatal(err)
	}
	rcv.checkPlatform = func() error { return nil }
	rcv.tree = tree
	rcv.open = open
	rcv.backoffMin, rcv.backoffMax = time.Millisecond, 5*time.Millisecond
	return rcv, sink
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestStartFailsOnUnsupportedPlatform(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the Windows build supports the receiver")
	}
	cfg := createDefaultConfig().(*Config)
	cfg.Paths = []string{`D:\CDSProjects`}
	rcv, err := createLogsReceiver(t.Context(), receivertest.NewNopSettings(component.MustNewType(typeStr)), cfg, &flakySink{})
	if err != nil {
		t.Fatal(err)
	}
	err = rcv.Start(t.Context(), componenttest.NewNopHost())
	if err == nil || !strings.Contains(err.Error(), "platform unsupported") {
		t.Fatalf("got %v", err)
	}
}

func TestStartFailsWhenStorageExtensionIsMissing(t *testing.T) {
	rcv, _ := newTestReceiver(t, newTree(), func(string, int) (watcher, error) { return newFakeWatcher(), nil })
	id := component.MustNewIDWithName("file_storage", "nope")
	rcv.cfg.StorageID = &id
	err := rcv.Start(t.Context(), componenttest.NewNopHost())
	if err == nil || !strings.Contains(err.Error(), "file_storage/nope") {
		t.Fatalf("want an error that names the storage ID, got %v", err)
	}
}

func TestStartFailsWhenExtensionIsNotStorage(t *testing.T) {
	rcv, _ := newTestReceiver(t, newTree(), func(string, int) (watcher, error) { return newFakeWatcher(), nil })
	id := component.MustNewID("health_check")
	rcv.cfg.StorageID = &id
	host := extHost{Host: componenttest.NewNopHost(), exts: map[component.ID]component.Component{id: plainExtension{}}}
	err := rcv.Start(t.Context(), host)
	if err == nil || !strings.Contains(err.Error(), "not a storage extension") {
		t.Fatalf("got %v", err)
	}
}

func TestEndToEndWithStorageAndShutdown(t *testing.T) {
	tr := newTree()
	w := newFakeWatcher()
	rcv, sink := newTestReceiver(t, tr, func(string, int) (watcher, error) { return w, nil })
	id := component.MustNewID("file_storage")
	rcv.cfg.StorageID = &id
	client := &storageClient{memStore: newStore()}
	host := extHost{Host: componenttest.NewNopHost(), exts: map[component.ID]component.Component{id: &storageExtension{client: client}}}

	if err := rcv.Start(t.Context(), host); err != nil {
		t.Fatal(err)
	}
	eventually(t, "first-start state", func() bool { client.mu.Lock(); defer client.mu.Unlock(); return len(client.data) == 1 })

	tr.put("run.dx", 9, time.Now().Add(-time.Second))
	w.msgs <- watchMsg{events: []rawEvent{{actionAdded, "run.dx"}}}
	eventually(t, "the record", func() bool { return len(records(sink)) == 1 })

	if err := rcv.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !w.closed.Load() || !client.closed.Load() {
		t.Fatalf("watcher closed %v, storage closed %v", w.closed.Load(), client.closed.Load())
	}
	st := client.saved(t, testRoot)
	if len(st.Recent) != 1 {
		t.Fatalf("shutdown must save the sentinel and recent set, got %+v", st)
	}
}

// stuckTree blocks in Walk, ignoring the context, until release is closed.
type stuckTree struct {
	*fakeTree
	entered chan struct{}
	release chan struct{}
}

func (s *stuckTree) Walk(context.Context, string, func(string) bool, func(fileInfo)) (walkStats, error) {
	select {
	case s.entered <- struct{}{}:
	default:
	}
	<-s.release
	return walkStats{}, nil
}

func TestShutdownReturnsWhenItsContextEnds(t *testing.T) {
	st := &stuckTree{fakeTree: newTree(), entered: make(chan struct{}, 1), release: make(chan struct{})}
	rcv, _ := newTestReceiver(t, st, func(string, int) (watcher, error) { return newFakeWatcher(), nil })
	if err := rcv.Start(t.Context(), componenttest.NewNopHost()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-st.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("the scan never started")
	}

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	err := rcv.Shutdown(ctx)
	close(st.release)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v, want the context deadline", err)
	}
	if err := rcv.Shutdown(t.Context()); err != nil {
		t.Fatalf("second shutdown after the scan ended: %v", err)
	}
}

func TestMissingRootAtStartIsRetried(t *testing.T) {
	tr := newTree()
	w := newFakeWatcher()
	var attempts atomic.Int32
	open := func(string, int) (watcher, error) {
		if attempts.Add(1) <= 3 {
			return nil, errors.New("the system cannot find the path specified")
		}
		return w, nil
	}
	rcv, sink := newTestReceiver(t, tr, open)
	tr.put("late.dx", 1, time.Now().Add(time.Minute))
	if err := rcv.Start(t.Context(), componenttest.NewNopHost()); err != nil {
		t.Fatalf("start must succeed with a missing root: %v", err)
	}
	t.Cleanup(func() { _ = rcv.Shutdown(context.Background()) })

	eventually(t, "the file found after the root opens", func() bool { return len(records(sink)) == 1 })
	if attempts.Load() < 4 {
		t.Fatalf("attempts %d", attempts.Load())
	}
}

func TestWatcherErrorReopensAndRescans(t *testing.T) {
	tr := newTree()
	var mu sync.Mutex
	var watchers []*fakeWatcher
	open := func(string, int) (watcher, error) {
		mu.Lock()
		defer mu.Unlock()
		w := newFakeWatcher()
		watchers = append(watchers, w)
		return w, nil
	}
	rcv, sink := newTestReceiver(t, tr, open)
	if err := rcv.Start(t.Context(), componenttest.NewNopHost()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rcv.Shutdown(context.Background()) })
	eventually(t, "first watcher and its scan", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(watchers) == 1 && tr.walkCount() >= 1
	})

	tr.put("while-down.dx", 1, time.Now().Add(time.Minute))
	mu.Lock()
	first := watchers[0]
	mu.Unlock()
	first.msgs <- watchMsg{err: errors.New("the volume is gone")}

	eventually(t, "a second watcher", func() bool { mu.Lock(); defer mu.Unlock(); return len(watchers) == 2 })
	eventually(t, "the rescan after reopening", func() bool { return len(records(sink)) == 1 })
	if !first.closed.Load() {
		t.Fatal("the dead watcher must be closed")
	}
}

func TestShutdownBeforeStartIsSafe(t *testing.T) {
	rcv, _ := newTestReceiver(t, newTree(), func(string, int) (watcher, error) { return newFakeWatcher(), nil })
	if err := rcv.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
}
