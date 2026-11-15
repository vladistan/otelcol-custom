// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

//go:build !windows

package fswatchreceiver

import "errors"

var errUnsupported = errors.New("platform unsupported: the fswatch receiver runs on Windows only")

func platformCheck() error { return errUnsupported }

func openWatcher(string, int) (watcher, error) { return nil, errUnsupported }
