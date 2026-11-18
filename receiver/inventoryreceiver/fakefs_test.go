// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package inventoryreceiver

import (
	"io/fs"
	"sort"
	"sync"
	"testing/fstest"
)

// testFS is a fixture tree. A path in deny fails with a permission error, a
// path in links reads as a symbolic link, and every opened name is recorded.
type testFS struct {
	files fstest.MapFS
	deny  map[string]bool
	links map[string]string

	mu     sync.Mutex
	opened map[string]bool
}

func newTestFS() *testFS {
	return &testFS{files: fstest.MapFS{}, deny: map[string]bool{}, links: map[string]string{}, opened: map[string]bool{}}
}

func (t *testFS) set(name, content string) *testFS {
	t.files[name] = &fstest.MapFile{Data: []byte(content)}
	return t
}

func (t *testFS) bytes(name string, data []byte) *testFS {
	t.files[name] = &fstest.MapFile{Data: data}
	return t
}

func (t *testFS) link(name, target string) *testFS {
	t.links[name] = target
	return t
}

func (t *testFS) denyPath(name string) *testFS {
	t.deny[name] = true
	return t
}

func (t *testFS) remove(name string) *testFS {
	delete(t.files, name)
	return t
}

func (t *testFS) Open(name string) (fs.File, error) {
	t.mu.Lock()
	t.opened[name] = true
	t.mu.Unlock()
	if t.deny[name] {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrPermission}
	}
	return t.files.Open(name)
}

func (t *testFS) Lstat(name string) (fs.FileInfo, error) { return fs.Stat(t.files, name) }

func (t *testFS) ReadLink(name string) (string, error) {
	if target, ok := t.links[name]; ok {
		return target, nil
	}
	return "", &fs.PathError{Op: "readlink", Path: name, Err: fs.ErrNotExist}
}

func (t *testFS) openedNames() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	var out []string
	for n := range t.opened {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
