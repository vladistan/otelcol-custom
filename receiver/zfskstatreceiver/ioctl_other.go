// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

//go:build !linux && !freebsd

package zfskstatreceiver

// openZFS has no control device to open on this system.
func openZFS(string) (zfsDevice, error) { return nil, errIoctlUnsupported }
