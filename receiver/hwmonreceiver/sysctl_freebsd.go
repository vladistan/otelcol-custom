// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

//go:build freebsd

package hwmonreceiver

import "golang.org/x/sys/unix"

// unixSysctl reads integer sysctls through x/sys/unix, with no exec and no cgo.
type unixSysctl struct{}

func (unixSysctl) uint32(name string) (uint32, error) {
	return unix.SysctlUint32(name)
}
