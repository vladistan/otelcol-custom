// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

//go:build !linux

package ethtoolreceiver

import "errors"

// newNIC fails off Linux: the statistics come from the Linux SIOCETHTOOL ioctl.
func newNIC() (nicReader, error) {
	return nil, errors.New("the ethtool receiver reads statistics with a Linux ioctl and is not available on this platform")
}
