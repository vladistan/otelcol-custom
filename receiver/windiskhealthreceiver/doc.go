// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

// Package windiskhealthreceiver reports physical disk health on Windows as the hw.* metrics the smart
// receiver gives on Linux. It calls DeviceIoControl in the collector process (LocalSystem is needed) and
// runs no external program. Disks behind a RAID controller or a driver that refuses the query are skipped.
// It emits no I/O rate or queue series. Other systems compile, and the factory reports Windows only.
package windiskhealthreceiver // import "github.com/vladistan/otelcol-custom/receiver/windiskhealthreceiver"
