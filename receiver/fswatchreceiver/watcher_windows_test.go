// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

//go:build windows

package fswatchreceiver

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/component/componenttest"
	"go.opentelemetry.io/collector/receiver/receivertest"
)

// next reads messages until one satisfies want, or the timeout passes.
func next(t *testing.T, w watcher, timeout time.Duration, want func(watchMsg) bool) (watchMsg, bool) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case m, ok := <-w.Messages():
			if !ok {
				return watchMsg{}, false
			}
			if want(m) {
				return m, true
			}
		case <-deadline:
			return watchMsg{}, false
		}
	}
}

func hasEvent(act action, name string) func(watchMsg) bool {
	return func(m watchMsg) bool {
		for _, e := range m.events {
			if e.action == act && e.name == name {
				return true
			}
		}
		return false
	}
}

func openRoot(t *testing.T, size int) (string, watcher) {
	t.Helper()
	root := t.TempDir()
	w, err := openWatcher(root, size)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Close() })
	return root, w
}

func TestWindowsWatcherSeesNestedCreate(t *testing.T) {
	root, w := openRoot(t, 64*1024)
	if err := os.MkdirAll(filepath.Join(root, "sub", "deep"), 0o755); err != nil {
		t.Fatal(err)
	}
	name := filepath.Join("sub", "deep", "run.dx")
	if err := os.WriteFile(filepath.Join(root, name), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := next(t, w, 5*time.Second, hasEvent(actionAdded, name)); !ok {
		t.Fatalf("no ADDED event for %s", name)
	}
}

func TestWindowsWatcherReportsTempToFinalRename(t *testing.T) {
	root, w := openRoot(t, 64*1024)
	tmp, final := filepath.Join(root, "~a.tmp"), filepath.Join(root, "a.dx")
	if err := os.WriteFile(tmp, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, final); err != nil {
		t.Fatal(err)
	}
	if _, ok := next(t, w, 5*time.Second, hasEvent(actionRenamedNew, "a.dx")); !ok {
		t.Fatal("no RENAMED_NEW event for a.dx")
	}
}

func TestWindowsWatcherCloseCancelsThePendingRead(t *testing.T) {
	_, w := openRoot(t, 64*1024)
	done := make(chan struct{})
	go func() { _ = w.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not return")
	}
	if _, ok := <-w.Messages(); ok {
		t.Fatal("the message channel must be closed after Close")
	}
}

func TestWindowsWatcherReportsOverflowOnATinyBuffer(t *testing.T) {
	root, w := openRoot(t, 4096)
	for i := 0; i < 3000; i++ {
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("burst-%04d-with-a-longish-name.dx", i)), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok := next(t, w, 10*time.Second, func(m watchMsg) bool { return m.overflow }); !ok {
		t.Fatal("no overflow reported")
	}
}

func TestWindowsWatcherEndsWhenTheRootIsRemoved(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "watched")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	w, err := openWatcher(root, 64*1024)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Close() })
	if err := os.Remove(root); err != nil {
		t.Fatalf("an open watch must not block removal of the root: %v", err)
	}
	m, ok := next(t, w, 5*time.Second, func(m watchMsg) bool { return m.err != nil })
	if !ok {
		t.Fatal("removing the root must end the watch with an error")
	}
	t.Logf("watch error after removal: %v", m.err)
}

func TestWindowsOpenFailsForAMissingRoot(t *testing.T) {
	if _, err := openWatcher(filepath.Join(t.TempDir(), "nope"), 64*1024); err == nil {
		t.Fatal("want an error")
	}
}

func TestWindowsCreationTimeIsNotTheModificationTime(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "a.dx")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	fi, err := osTree{}.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(fi.created) > time.Minute {
		t.Fatalf("creation time %v must be about now, not the set modification time", fi.created)
	}
}

// The next two tests record what the host does; they pass whichever way it goes.
func TestWindowsMovedInDirectoryReport(t *testing.T) {
	root, w := openRoot(t, 64*1024)
	outside := t.TempDir()
	dir := filepath.Join(outside, "run1")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "f.dx"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(dir, filepath.Join(root, "run1")); err != nil {
		t.Skipf("move across volumes: %v", err)
	}
	m, ok := next(t, w, 5*time.Second, func(m watchMsg) bool { return len(m.events) > 0 })
	t.Logf("move-in events (seen=%v): %+v", ok, m.events)
}

func TestWindowsTunnelingReport(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "t.dx")
	if err := os.WriteFile(path, []byte("1"), 0o600); err != nil {
		t.Fatal(err)
	}
	first, _ := osTree{}.Stat(path)
	time.Sleep(1100 * time.Millisecond)
	_ = os.Remove(path)
	_ = os.WriteFile(path, []byte("2"), 0o600)
	second, _ := osTree{}.Stat(path)
	t.Logf("creation time kept after delete and recreate within 15 s: %v (first %v, second %v)",
		first.created.Equal(second.created), first.created, second.created)
}

func TestWindowsReceiverEndToEnd(t *testing.T) {
	root := t.TempDir()
	cfg := createDefaultConfig().(*Config)
	cfg.Paths = []string{root}
	sink := &flakySink{}
	rcv, err := createLogsReceiver(t.Context(), receivertest.NewNopSettings(component.MustNewType(typeStr)), cfg, sink)
	if err != nil {
		t.Fatal(err)
	}
	if err := rcv.Start(t.Context(), componenttest.NewNopHost()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rcv.Shutdown(t.Context()) })
	time.Sleep(500 * time.Millisecond)

	if err := os.WriteFile(filepath.Join(root, "sub.dx"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the record for sub.dx", func() bool { return len(records(sink)) == 1 })
	got := records(sink)[0]
	if got.attrs["file.name"] != "sub.dx" || got.attrs["vstan.fswatch.catchup"] != false {
		t.Fatalf("attrs %v", got.attrs)
	}
}
