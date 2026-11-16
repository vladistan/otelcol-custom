// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

const (
	modeChar = syscall.S_IFCHR | 0o600
	modeReg  = syscall.S_IFREG | 0o644
	modeFifo = syscall.S_IFIFO | 0o600
)

// fakeFS maps paths to modes; a missing path is ENOENT. openedMode overrides what fstat reports.
type fakeFS struct {
	modes      map[string]uint32
	openedMode map[string]uint32
	opened     []string
	closed     int
	openErr    error
}

func (f *fakeFS) ops() nodeOps {
	return nodeOps{
		stat: func(p string) (uint32, error) {
			m, ok := f.modes[p]
			if !ok {
				return 0, syscall.ENOENT
			}
			return m, nil
		},
		open: func(p string) (int, error) {
			if f.openErr != nil {
				return -1, f.openErr
			}
			f.opened = append(f.opened, p)
			return len(f.opened), nil
		},
		fstat: func(fd int) (uint32, error) {
			p := f.opened[fd-1]
			if m, ok := f.openedMode[p]; ok {
				return m, nil
			}
			return f.modes[p], nil
		},
		close: func(int) { f.closed++ },
	}
}

func TestProbePrefersTheFirstCharacterDevice(t *testing.T) {
	fs := &fakeFS{modes: map[string]uint32{probePaths[0]: modeChar, probePaths[1]: modeChar}}

	_, path, err := selectNode(fs.ops(), "")

	if err != nil || path != probePaths[0] {
		t.Fatalf("path = %q, err = %v", path, err)
	}
}

func TestProbeFallsBackToTheSecondPath(t *testing.T) {
	fs := &fakeFS{modes: map[string]uint32{probePaths[1]: modeChar}}

	_, path, err := selectNode(fs.ops(), "")

	if err != nil || path != probePaths[1] {
		t.Fatalf("path = %q, err = %v", path, err)
	}
}

func TestProbeSkipsANonCharacterFirstPath(t *testing.T) {
	fs := &fakeFS{modes: map[string]uint32{probePaths[0]: modeReg, probePaths[1]: modeChar}}

	_, path, err := selectNode(fs.ops(), "")

	if err != nil || path != probePaths[1] {
		t.Fatalf("path = %q, err = %v", path, err)
	}
	if len(fs.opened) != 1 || fs.opened[0] != probePaths[1] {
		t.Errorf("opened %v; the regular file must not be opened", fs.opened)
	}
}

func TestProbeWithNoNodeListsEveryPathTried(t *testing.T) {
	fs := &fakeFS{modes: map[string]uint32{}}

	_, _, err := selectNode(fs.ops(), "")

	if err == nil {
		t.Fatal("expected an error")
	}
	for _, p := range probePaths {
		if !strings.Contains(err.Error(), p) {
			t.Errorf("error %q does not name %s", err, p)
		}
	}
}

func TestExplicitPathIsTheOnlyOneTried(t *testing.T) {
	fs := &fakeFS{modes: map[string]uint32{probePaths[0]: modeChar, "/x/node": modeChar}}

	_, path, err := selectNode(fs.ops(), "/x/node")

	if err != nil || path != "/x/node" {
		t.Fatalf("path = %q, err = %v", path, err)
	}
}

func TestExplicitMissingPathDoesNotFallBack(t *testing.T) {
	fs := &fakeFS{modes: map[string]uint32{probePaths[0]: modeChar}}

	_, _, err := selectNode(fs.ops(), "/x/node")

	if err == nil || !strings.Contains(err.Error(), "/x/node") || len(fs.opened) != 0 {
		t.Fatalf("err = %v, opened = %v", err, fs.opened)
	}
}

func TestExplicitNonCharacterPathIsRefusedWithPathAndMode(t *testing.T) {
	for name, mode := range map[string]uint32{"regular": modeReg, "fifo": modeFifo} {
		fs := &fakeFS{modes: map[string]uint32{"/x/node": mode}}

		_, _, err := selectNode(fs.ops(), "/x/node")

		if err == nil || !strings.Contains(err.Error(), "/x/node") || !strings.Contains(err.Error(), "mode") {
			t.Errorf("%s: err = %v", name, err)
		}
		if len(fs.opened) != 0 {
			t.Errorf("%s: a non-character path was opened", name)
		}
	}
}

func TestOpenedDescriptorThatIsNotACharacterDeviceIsRefusedAndClosed(t *testing.T) {
	fs := &fakeFS{
		modes:      map[string]uint32{"/x/link": modeChar},
		openedMode: map[string]uint32{"/x/link": modeReg},
	}

	_, _, err := selectNode(fs.ops(), "/x/link")

	if err == nil || !strings.Contains(err.Error(), "not a character device") {
		t.Fatalf("err = %v", err)
	}
	if fs.closed != 1 {
		t.Errorf("closed = %d, want 1", fs.closed)
	}
}

func TestOpenPermissionErrorNamesTheCapability(t *testing.T) {
	fs := &fakeFS{modes: map[string]uint32{probePaths[1]: modeChar}, openErr: syscall.EACCES}

	_, _, err := selectNode(fs.ops(), "")

	if !errors.Is(err, errNeedsCapability) {
		t.Fatalf("err = %v", err)
	}
}

func TestNullDeviceIsAcceptedAsACharacterDevice(t *testing.T) {
	fi, err := os.Stat("/dev/null")
	if err != nil || fi.Mode()&os.ModeCharDevice == 0 {
		t.Skip("no character device /dev/null")
	}
	ops := nodeOps{
		stat:  func(string) (uint32, error) { return modeChar, nil },
		open:  func(string) (int, error) { return 3, nil },
		fstat: func(int) (uint32, error) { return modeChar, nil },
		close: func(int) {},
	}

	_, path, err := selectNode(ops, "/dev/null")

	if err != nil || path != "/dev/null" {
		t.Fatalf("path = %q, err = %v", path, err)
	}
}

type namedDevice struct {
	device
	node string
}

func (n namedDevice) nodePath() string { return n.node }

func TestRunPassesDevToTheOpenerAndRecordsTheNodeInTheManifest(t *testing.T) {
	te := newTestEnv(t, map[int]string{0: "megaraid_sas"})
	var got string
	te.env.open = func(devPath string, _ int) (device, func(), error) {
		got = devPath
		return namedDevice{newFakeController(), "/x/node"}, func() {}, nil
	}
	out := filepath.Join(t.TempDir(), "o")

	code := te.run("-out", out, "-label", "lab", "-dev", "/x/node")

	if code != 0 || got != "/x/node" {
		t.Fatalf("exit = %d, dev = %q, stderr = %q", code, got, te.stderr.String())
	}
	body, err := os.ReadFile(filepath.Join(out, "lab-host0", "manifest.json"))
	if err != nil || !strings.Contains(string(body), `"device": "/x/node"`) {
		t.Fatalf("manifest = %s, err = %v", body, err)
	}
}

func TestRunWithoutDevLeavesTheProbeToTheOpener(t *testing.T) {
	te := newTestEnv(t, map[int]string{0: "megaraid_sas"})
	got := "unset"
	te.env.open = func(devPath string, _ int) (device, func(), error) {
		got = devPath
		return newFakeController(), func() {}, nil
	}

	if code := te.run("-out", filepath.Join(t.TempDir(), "o")); code != 0 || got != "" {
		t.Fatalf("exit = %d, dev = %q", code, got)
	}
}

func TestRunRefusesAnOutputDirectoryThatIsASymlink(t *testing.T) {
	te := newTestEnv(t, map[int]string{0: "megaraid_sas"})
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	link := filepath.Join(dir, "link")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skip("no symlinks")
	}

	code := te.run("-out", link)

	if code != 1 || !strings.Contains(te.stderr.String(), "symbolic link") {
		t.Fatalf("exit = %d, stderr = %q", code, te.stderr.String())
	}
	if entries, _ := os.ReadDir(target); len(entries) != 0 {
		t.Error("something was written through the link")
	}
}
