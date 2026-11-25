// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

// Package ippreceiver polls printers over IPP and reports their state, reasons,
// marker levels, job acceptance and queue length as gauges named printer.ipp.*.
//
// Each printer gets one read-only Get-Printer-Attributes request per interval, sent
// from the collector process; no external command runs. The printer list is static.
package ippreceiver // import "github.com/vladistan/otelcol-custom/receiver/ippreceiver"
