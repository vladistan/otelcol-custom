// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

//go:build !windows

package windiskhealthreceiver

func newSystemBus() (bus, error) { return nil, errSystemOnly }
