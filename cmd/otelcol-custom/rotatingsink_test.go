// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
)

func sinkURL(t *testing.T, path string) string {
	t.Helper()
	return "rotating:///" + strings.TrimPrefix(filepath.ToSlash(path), "/")
}

func countFiles(t *testing.T, dir string) int {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	return len(entries)
}

func TestRotatingLoggerLimits(t *testing.T) {
	l := newRotatingLogger("x.log")
	if l.MaxSize != 10 {
		t.Errorf("MaxSize = %d MiB, want 10", l.MaxSize)
	}
	if l.MaxBackups != 5 {
		t.Errorf("MaxBackups = %d, want 5", l.MaxBackups)
	}
}

func TestRotatingSinkRotatesAndKeepsFiveBackups(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "self.log")
	u, err := url.Parse(sinkURL(t, path))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	sink, err := newRotatingSink(u)
	if err != nil {
		t.Fatalf("newRotatingSink: %v", err)
	}
	chunk := []byte(strings.Repeat("x", 1<<20))
	for i := 0; i < 85; i++ {
		if _, err := sink.Write(chunk); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}
	if err := sink.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for countFiles(t, dir) > 6 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if got := countFiles(t, dir); got != 6 {
		t.Errorf("files = %d, want 6 (live + 5 backups)", got)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat live file: %v", err)
	}
	if info.Size() > 10<<20 {
		t.Errorf("live file %d bytes exceeds 10 MiB", info.Size())
	}
}

func TestRotatingSinkUnopenablePathFailsLoggerBuild(t *testing.T) {
	if err := registerRotatingSink(); err != nil {
		t.Fatalf("register: %v", err)
	}
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatalf("write blocker: %v", err)
	}
	cfg := zap.NewProductionConfig()
	cfg.OutputPaths = []string{sinkURL(t, filepath.Join(blocker, "sub", "self.log"))}
	_, err := cfg.Build()
	if err == nil {
		t.Fatal("Build succeeded on an unopenable path, want error")
	}
	if !strings.Contains(err.Error(), "create log directory") {
		t.Errorf("error = %q, want it to name the log directory failure", err)
	}
}

func TestRotatingSinkWritesThroughZap(t *testing.T) {
	if err := registerRotatingSink(); err != nil {
		t.Fatalf("register: %v", err)
	}
	path := filepath.Join(t.TempDir(), "nested", "self.log")
	cfg := zap.NewProductionConfig()
	cfg.OutputPaths = []string{sinkURL(t, path)}
	logger, err := cfg.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	logger.Info("hello sink")
	if err := logger.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(got), "hello sink") {
		t.Errorf("log file %q lacks the message", got)
	}
}

func TestRegisterRotatingSinkIsIdempotent(t *testing.T) {
	for i := 0; i < 2; i++ {
		if err := registerRotatingSink(); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
}

func TestRotatingPath(t *testing.T) {
	cases := []struct {
		name   string
		raw    string
		want   string
		errSub string
	}{
		{"unix absolute", "rotating:///var/log/otel/self.log", filepath.FromSlash("/var/log/otel/self.log"), ""},
		{"windows drive", "rotating:///C:/ProgramData/otel/self.log", filepath.FromSlash("C:/ProgramData/otel/self.log"), ""},
		{"host present", "rotating://logs/self.log", "", `got host "logs"`},
		{"empty path", "rotating:", "", "has no file path"},
		{"root only", "rotating:///", "", "has no file path"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u, err := url.Parse(tc.raw)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			got, err := rotatingPath(u)
			if tc.errSub == "" && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.errSub != "" && (err == nil || !strings.Contains(err.Error(), tc.errSub)) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.errSub)
			}
			if got != tc.want {
				t.Errorf("path = %q, want %q", got, tc.want)
			}
		})
	}
}
