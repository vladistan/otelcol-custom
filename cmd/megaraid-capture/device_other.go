// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

//go:build !linux

package main

import "errors"

func openNode(string, int) (device, func(), error) {
	return nil, nil, errors.New("the MegaRAID ioctl node exists on Linux only")
}
