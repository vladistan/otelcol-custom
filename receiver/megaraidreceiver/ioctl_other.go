// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

//go:build !linux

package megaraidreceiver

import "errors"

// openNode always fails off Linux; the factory refuses the receiver there.
func openNode(int, bool) (device, func(), error) {
	return nil, nil, errors.New("the megaraid receiver reads /dev/megaraid_sas_ioctl_node and runs only on Linux")
}
