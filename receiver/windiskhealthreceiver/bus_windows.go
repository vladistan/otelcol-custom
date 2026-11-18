// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

//go:build windows

package windiskhealthreceiver

import "time"

// ioctlBus joins the platform calls that list and open disks to the request logic that is the same on
// every system.
type ioctlBus struct {
	listFn func() ([]string, error)
	openFn func(name string, timeout time.Duration) (controller, error)
}

func (b *ioctlBus) discover() ([]string, error) { return b.listFn() }

func (b *ioctlBus) open(name string, timeout time.Duration) (session, error) {
	c, err := b.openFn(name, timeout)
	if err != nil {
		return nil, err
	}
	return &diskSession{c: c, timeout: timeout}, nil
}
