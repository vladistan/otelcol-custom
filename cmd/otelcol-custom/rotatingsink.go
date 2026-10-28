// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sync"

	"go.uber.org/zap"
	"gopkg.in/natefinch/lumberjack.v2"
)

const (
	rotatingScheme     = "rotating"
	rotatingMaxSizeMiB = 10
	rotatingMaxBackups = 5
)

var (
	registerOnce sync.Once
	registerErr  error

	driveLetterPath = regexp.MustCompile(`^/[A-Za-z]:/`)
)

// registerRotatingSink registers the zap scheme "rotating"; safe to call twice.
func registerRotatingSink() error {
	registerOnce.Do(func() {
		registerErr = zap.RegisterSink(rotatingScheme, newRotatingSink)
	})
	return registerErr
}

// rotatingPath turns rotating:///C:/dir/file or rotating:///var/dir/file into a
// file path.
func rotatingPath(u *url.URL) (string, error) {
	if u.Host != "" {
		return "", fmt.Errorf("rotating sink needs rotating:///absolute/path, got host %q", u.Host)
	}
	p := u.Path
	if driveLetterPath.MatchString(p) {
		p = p[1:]
	}
	if p == "" || p == "/" {
		return "", fmt.Errorf("rotating sink URL %q has no file path", u.String())
	}
	return filepath.FromSlash(p), nil
}

func newRotatingLogger(path string) *lumberjack.Logger {
	return &lumberjack.Logger{
		Filename:   path,
		MaxSize:    rotatingMaxSizeMiB,
		MaxBackups: rotatingMaxBackups,
	}
}

type rotatingSink struct {
	*lumberjack.Logger
}

// Sync is a no-op: every write already reaches the file.
func (rotatingSink) Sync() error { return nil }

// newRotatingSink opens the path once so an unusable path fails logger build;
// lumberjack alone would defer the error to the first write.
func newRotatingSink(u *url.URL) (zap.Sink, error) {
	path, err := rotatingPath(u)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, fmt.Errorf("create log directory: %w", err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o640)
	if err != nil {
		return nil, fmt.Errorf("open log file: %w", err)
	}
	if err := f.Close(); err != nil {
		return nil, fmt.Errorf("close log file probe: %w", err)
	}
	return rotatingSink{newRotatingLogger(path)}, nil
}
