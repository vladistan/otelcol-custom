// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package fswatchreceiver

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"
	"time"
)

func writeFile(t *testing.T, path string, size int, mtime time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, size), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

func TestOSTreeWalk(t *testing.T) {
	root := t.TempDir()
	when := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	writeFile(t, filepath.Join(root, "a.dx"), 3, when)
	writeFile(t, filepath.Join(root, "sub", "b.dx"), 5, when)
	writeFile(t, filepath.Join(root, "cache", "c.bin"), 7, when)

	var got []string
	stats, err := osTree{}.Walk(t.Context(), root, func(rel string) bool { return rel == "cache" }, func(f fileInfo) {
		got = append(got, f.path)
		if f.size == 0 {
			t.Errorf("size of %s", f.path)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(got)
	want := []string{filepath.Join(root, "a.dx"), filepath.Join(root, "sub", "b.dx")}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("got %v, want %v", got, want)
	}
	if stats.denied != 0 {
		t.Fatalf("denied %d", stats.denied)
	}
}

func TestOSTreeStat(t *testing.T) {
	root := t.TempDir()
	when := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	file := filepath.Join(root, "a.dx")
	writeFile(t, file, 11, when)

	fi, err := osTree{}.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	if fi.size != 11 || fi.isDir || fi.path != file {
		t.Fatalf("got %+v", fi)
	}
	dir, err := osTree{}.Stat(root)
	if err != nil || !dir.isDir {
		t.Fatalf("dir %+v err %v", dir, err)
	}
	if _, err := (osTree{}).Stat(filepath.Join(root, "missing")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("want ErrNotExist, got %v", err)
	}
}

func TestOSTreeCountsDeniedDirectories(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("permission bits are not enforced here")
	}
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "ok.dx"), 1, time.Now())
	locked := filepath.Join(root, "locked")
	if err := os.MkdirAll(locked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	n := 0
	stats, err := osTree{}.Walk(t.Context(), root, nil, func(fileInfo) { n++ })
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 || stats.denied != 1 {
		t.Fatalf("files %d denied %d", n, stats.denied)
	}
}

func TestOSTreeWalkStopsWhenContextEnds(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.dx"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	n := 0
	if _, err := (osTree{}).Walk(ctx, root, nil, func(fileInfo) { n++ }); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled", err)
	}
	if n != 0 {
		t.Fatalf("reported %d files after cancel", n)
	}
}
