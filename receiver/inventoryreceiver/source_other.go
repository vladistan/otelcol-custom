// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

//go:build !linux && !windows

package inventoryreceiver

import "errors"

var errNotSupported = errors.New("the inventory receiver reads Linux and Windows hosts only")

// newSource fails off Linux and Windows: there is no source to read.
func newSource(*Config) (source, error) {
	return nil, errNotSupported
}
