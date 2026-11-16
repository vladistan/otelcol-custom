// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

// Package hwmonreceiver reports the temperature, fan and voltage sensors of
// the host with the hw.* metric names of the OpenTelemetry semantic
// conventions: hw.temperature, hw.temperature.limit, hw.fan.speed and
// hw.voltage.
//
// On Linux it reads /sys/class/hwmon and the thermal zones below
// /sys/class/thermal; on FreeBSD it reads the CPU and ACPI temperature
// sysctls. Both are read in the collector process, with no external command,
// no cgo and no capability. The receiver builds on Linux and FreeBSD only.
package hwmonreceiver // import "github.com/vladistan/otelcol-custom/receiver/hwmonreceiver"
