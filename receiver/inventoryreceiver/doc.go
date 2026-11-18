// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

// Package inventoryreceiver reports host hardware (CPU, memory, board, BIOS,
// system, drives, GPU) as info gauges of value 1 with the data in attributes.
// Linux sources are /proc, /sys and SMBIOS; Windows sources are SMBIOS, the
// registry and drive storage properties. It runs no external program and uses
// no cgo. Virtual machines and containers report nothing.
package inventoryreceiver // import "github.com/vladistan/otelcol-custom/receiver/inventoryreceiver"
