// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

// Package processlifetimeprocessor adds two gauges to process metrics, each the
// ratio of a cumulative total to the age of the process:
//
//   - process.cpu.lifetime.utilization: process.cpu.time over all states divided
//     by process.uptime.
//   - process.disk.io.lifetime.rate: process.disk.io per direction divided by
//     process.uptime, in bytes per second.
//
// A resource without a positive process.uptime gets neither gauge. The host
// metrics process scraper must have process.uptime enabled.
package processlifetimeprocessor // import "github.com/vladistan/otelcol-custom/processor/processlifetimeprocessor"
