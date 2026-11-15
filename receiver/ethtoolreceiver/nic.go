// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package ethtoolreceiver

import (
	"errors"
	"time"
)

// The reader maps the ioctl errors the receiver treats differently to these.
var (
	errNotSupported = errors.New("operation not supported by the interface")
	errNoDevice     = errors.New("no such interface")
	errPermission   = errors.New("permission denied")
	errCountChanged = errors.New("statistic count changed during the read")
)

// nicReader is the ioctl side of the receiver; tests replace it.
type nicReader interface {
	interfaces() ([]string, error)
	driver(iface string) (string, error)
	statCount(iface string) (int, error)
	// statNames and statValues return the count the kernel reports, which can
	// differ from count when the interface was reset between the calls.
	statNames(iface string, count int) ([]string, error)
	statValues(iface string, count int) ([]uint64, error)
	// bootTime is when the host started; the counters count from the driver load.
	bootTime() (time.Time, error)
	close() error
}
