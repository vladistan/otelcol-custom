// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

//go:build !windows

package fswatchreceiver

import (
	"io/fs"
	"time"
)

// birthTime has no portable source off Windows; unit tests set the modification time.
func birthTime(fi fs.FileInfo) time.Time { return fi.ModTime() }
