// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

// Package cloudwatchmetricsreceiver reads CloudWatch metrics for many accounts,
// regions and tiers. It lists the series of each named metric, reads the
// explicit statistics of each tier with GetMetricData, and keeps the end of the
// last window so a restart neither repeats nor skips one.
package cloudwatchmetricsreceiver
