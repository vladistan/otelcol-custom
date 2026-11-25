// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package filestorage

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.etcd.io/bbolt"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/extension/extensiontest"
	"go.opentelemetry.io/collector/extension/xextension/storage"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

const proofEnv = "FILESTORAGE_PROOF_OPEN"

var corruptName = regexp.MustCompile(`\.corrupt-[0-9]{8}T[0-9]{6}\.[0-9]{3}Z$`)

// init makes the test binary the child of the proof test: it opens the file the way the extension does,
// with a recover() around the call.
func init() {
	path := os.Getenv(proofEnv)
	if path == "" {
		return
	}
	defer func() {
		if recover() != nil {
			fmt.Println("RECOVERED")
			os.Exit(0)
		}
	}()
	db, err := bbolt.Open(path, 0o600, &bbolt.Options{NoFreelistSync: true, FreelistType: bbolt.FreelistMapType})
	if err == nil {
		// The first write transaction loads the freelist, as newClient does.
		_ = db.Update(func(tx *bbolt.Tx) error {
			_, err := tx.CreateBucketIfNotExists([]byte("default"))
			return err
		})
		_ = db.Close()
	}
	fmt.Println("OPENED")
	os.Exit(0)
}

// writeBufferFile makes a bbolt file with the extension's bucket and enough keys for several pages.
func writeBufferFile(t *testing.T, path string) {
	t.Helper()
	db, err := bbolt.Open(path, 0o600, &bbolt.Options{NoFreelistSync: true, FreelistType: bbolt.FreelistMapType})
	require.NoError(t, err)
	require.NoError(t, db.Update(func(tx *bbolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte("default"))
		if err != nil {
			return err
		}
		for i := range 2000 {
			if err := b.Put([]byte(fmt.Sprintf("key-%04d", i)), bytes.Repeat([]byte("v"), 100)); err != nil {
				return err
			}
		}
		return nil
	}))
	require.NoError(t, db.Close())
}

// garblePageIDs overwrites the page id of every page after the freelist, so a page does not identify as
// the page it sits on (the state after a hard reset).
func garblePageIDs(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	pageSize := os.Getpagesize()
	require.Greater(t, len(data), 4*pageSize, "the fixture needs several pages")
	for off := 3 * pageSize; off+8 <= len(data); off += pageSize {
		binary.LittleEndian.PutUint64(data[off:], 0xdeadbeef)
	}
	require.NoError(t, os.WriteFile(path, data, 0o600))
}

func garbledFixture(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "buffer.db")
	writeBufferFile(t, path)
	garblePageIDs(t, path)
	return path
}

func TestRecoverInTheCaller_DoesNotCatchTheBboltPagePanic(t *testing.T) {
	exe, err := os.Executable()
	require.NoError(t, err)
	cmd := exec.Command(exe)
	cmd.Env = append(os.Environ(), proofEnv+"="+garbledFixture(t))
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr

	err = cmd.Run()

	var exitErr *exec.ExitError
	require.ErrorAs(t, err, &exitErr, "stdout: %s", stdout.String())
	assert.NotContains(t, stdout.String(), "RECOVERED")
	assert.Contains(t, stderr.String(), "assertion failed: Page expected to be")
}

func TestCheckBufferFile(t *testing.T) {
	for name, c := range map[string]struct {
		prepare func(t *testing.T, path string)
		corrupt bool
		detail  string
	}{
		"valid file": {prepare: writeBufferFile},
		"page ids garbled": {
			prepare: func(t *testing.T, path string) {
				writeBufferFile(t, path)
				garblePageIDs(t, path)
			},
			corrupt: true,
			detail:  "Page expected to be",
		},
		"not a bbolt file": {
			prepare: func(t *testing.T, path string) {
				require.NoError(t, os.WriteFile(path, bytes.Repeat([]byte("x"), 3*os.Getpagesize()), 0o600))
			},
			corrupt: true,
		},
		"missing file": {prepare: func(*testing.T, string) {}},
		"empty file": {
			prepare: func(t *testing.T, path string) { require.NoError(t, os.WriteFile(path, nil, 0o600)) },
		},
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "buffer.db")
			c.prepare(t, path)

			corrupt, detail, err := checkBufferFile(t.Context(), path, 30*time.Second)

			require.NoError(t, err)
			assert.Equal(t, c.corrupt, corrupt)
			assert.Contains(t, detail, c.detail)
		})
	}
}

func newRecreateExtension(t *testing.T, dir string, recreate bool) (storage.Extension, *observer.ObservedLogs) {
	t.Helper()
	f := NewFactory()
	cfg := f.CreateDefaultConfig().(*Config)
	cfg.Directory = dir
	cfg.Recreate = recreate
	core, logs := observer.New(zap.DebugLevel)
	set := extensiontest.NewNopSettings(f.Type())
	set.Logger = zap.New(core)
	ext, err := f.Create(t.Context(), set, cfg)
	require.NoError(t, err)
	se, ok := ext.(storage.Extension)
	require.True(t, ok)
	return se, logs
}

// firstRun writes a key through the extension, closes it and returns the path of the only file.
func firstRun(t *testing.T, dir string) string {
	t.Helper()
	se, _ := newRecreateExtension(t, dir, true)
	client, err := se.GetClient(t.Context(), component.KindReceiver, newTestEntity("buffer"), "")
	require.NoError(t, err)
	ops := []*storage.Operation{storage.SetOperation("key", []byte("val"))}
	for i := range 2000 {
		ops = append(ops, storage.SetOperation(fmt.Sprintf("filler-%04d", i), bytes.Repeat([]byte("v"), 100)))
	}
	require.NoError(t, client.Batch(t.Context(), ops...))
	require.NoError(t, client.Close(t.Context()))
	files, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, files, 1)
	return filepath.Join(dir, files[0].Name())
}

func TestGetClient_GarbledFileWithRecreate_KeepsTheFileAndStartsOnANewOne(t *testing.T) {
	dir := t.TempDir()
	path := firstRun(t, dir)
	garblePageIDs(t, path)
	garbled, err := os.ReadFile(path)
	require.NoError(t, err)
	se, logs := newRecreateExtension(t, dir, true)

	client, err := se.GetClient(t.Context(), component.KindReceiver, newTestEntity("buffer"), "")

	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, client.Close(t.Context())) })
	files, err := os.ReadDir(dir)
	require.NoError(t, err)
	var kept []string
	for _, f := range files {
		if corruptName.MatchString(f.Name()) {
			kept = append(kept, filepath.Join(dir, f.Name()))
		}
	}
	require.Len(t, kept, 1, "files: %v", files)
	assert.True(t, strings.HasPrefix(kept[0], path+".corrupt-"))
	content, err := os.ReadFile(kept[0])
	require.NoError(t, err)
	assert.Equal(t, garbled, content, "the corrupt file is kept as it was")
	val, err := client.Get(t.Context(), "key")
	require.NoError(t, err)
	assert.Nil(t, val, "the new file is empty")
	require.NoError(t, client.Set(t.Context(), "after", []byte("ok")))
	val, err = client.Get(t.Context(), "after")
	require.NoError(t, err)
	assert.Equal(t, []byte("ok"), val)
	assert.NotEmpty(t, logs.FilterMessage("buffer file is corrupt, recreating it").All())
}

func TestGetClient_NotABboltFileWithRecreate_IsRenamedAndRecreated(t *testing.T) {
	dir := t.TempDir()
	path := firstRun(t, dir)
	require.NoError(t, os.WriteFile(path, bytes.Repeat([]byte("x"), 3*os.Getpagesize()), 0o600))
	se, _ := newRecreateExtension(t, dir, true)

	client, err := se.GetClient(t.Context(), component.KindReceiver, newTestEntity("buffer"), "")

	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, client.Close(t.Context())) })
	matches, err := filepath.Glob(path + ".corrupt-*")
	require.NoError(t, err)
	assert.Len(t, matches, 1)
}

func TestGetClient_ValidFileWithRecreate_OpensAsBeforeAndIsNotRenamed(t *testing.T) {
	dir := t.TempDir()
	path := firstRun(t, dir)
	se, logs := newRecreateExtension(t, dir, true)

	client, err := se.GetClient(t.Context(), component.KindReceiver, newTestEntity("buffer"), "")

	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, client.Close(t.Context())) })
	val, err := client.Get(t.Context(), "key")
	require.NoError(t, err)
	assert.Equal(t, []byte("val"), val)
	matches, err := filepath.Glob(path + ".corrupt-*")
	require.NoError(t, err)
	assert.Empty(t, matches)
	assert.Empty(t, logs.FilterMessage("buffer file is corrupt, recreating it").All())
}
