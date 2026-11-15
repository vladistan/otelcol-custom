// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package fswatchreceiver

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

type fileInfo struct {
	path    string
	size    int64
	created time.Time
	// modified is the last write time; zero when unknown.
	modified time.Time
	isDir    bool
}

type walkStats struct{ denied int }

type tree interface {
	// Walk calls fn for each file under root. skipDir gets a root-relative directory path and may prune it.
	// It stops with the context's error once ctx ends.
	Walk(ctx context.Context, root string, skipDir func(rel string) bool, fn func(fileInfo)) (walkStats, error)
	Stat(path string) (fileInfo, error)
}

type osTree struct{}

func (osTree) Walk(ctx context.Context, root string, skipDir func(string) bool, fn func(fileInfo)) (walkStats, error) {
	var stats walkStats
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if err != nil {
			if path == root {
				return err
			}
			stats.denied++
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if path == root || skipDir == nil {
				return nil
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return relErr
			}
			if skipDir(rel) {
				return fs.SkipDir
			}
			return nil
		}
		info, infoErr := d.Info()
		switch {
		case errors.Is(infoErr, fs.ErrNotExist):
			return nil
		case infoErr != nil:
			stats.denied++
			return nil
		case !info.Mode().IsRegular():
			return nil
		}
		fn(fileInfo{path: path, size: info.Size(), created: birthTime(info), modified: info.ModTime()})
		return nil
	})
	return stats, err
}

func (osTree) Stat(path string) (fileInfo, error) {
	info, err := os.Stat(path)
	if err != nil {
		return fileInfo{}, err
	}
	return fileInfo{path: path, size: info.Size(), created: birthTime(info), modified: info.ModTime(), isDir: info.IsDir()}, nil
}
