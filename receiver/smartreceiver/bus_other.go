// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

//go:build !linux

package smartreceiver

import "errors"

func newSystemBus() (bus, error) {
	return nil, errors.New("the smart receiver reads disks with Linux ioctls and is not available on this platform")
}
