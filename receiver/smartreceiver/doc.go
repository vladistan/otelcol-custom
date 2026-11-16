// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

// Package smartreceiver reports the health of NVMe and SATA disks as hw.* metrics: status, temperature,
// endurance, errors, power-on time, power cycles, data moved, size and the SMART attribute table.
//
// The receiver sends SG_IO and NVMe admin ioctls from the collector process, one disk at a time, with
// golang.org/x/sys/unix; it runs no external command and uses no cgo. The commands are read only and
// pass an allow list; SMART is never enabled. Reading needs root, or CAP_SYS_ADMIN for NVMe and
// CAP_SYS_RAWIO for SATA. Disks behind a RAID controller are not read. The receiver builds on Linux only.
package smartreceiver // import "github.com/vladistan/otelcol-custom/receiver/smartreceiver"
