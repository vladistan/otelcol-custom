// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

//go:build !freebsd

package zfskstatreceiver

import (
	"syscall"
	"time"
)

// realSysctl has no sysctl ZFS tree to read here: every call reports not found.
func realSysctl() sysctlFns {
	return sysctlFns{
		lookupOID: func(string) ([]int32, error) { return nil, syscall.ENOENT },
		nextOID:   func([]int32) ([]int32, error) { return nil, syscall.ENOENT },
		oidName:   func([]int32) (string, error) { return "", syscall.ENOENT },
		getUint64: func(string) (uint64, error) { return 0, syscall.ENOENT },
		getString: func(string) (string, error) { return "", syscall.ENOENT },
		bootTime:  func() (time.Time, error) { return time.Time{}, syscall.ENOENT },
		getfsstat: func() ([]mount, error) { return nil, syscall.ENOENT },
	}
}
