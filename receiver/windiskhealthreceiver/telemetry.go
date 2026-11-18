// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package windiskhealthreceiver

import (
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/otel/metric"
)

const (
	scopeName      = "github.com/vladistan/otelcol-custom/receiver/windiskhealthreceiver"
	skippedMetric  = "otelcol_windiskhealth_devices_skipped"
	reasonLabelKey = "reason"
)

// Reasons a disk or one step of a disk is skipped in one interval.
const (
	reasonTimeout     = "timeout"
	reasonUnsupported = "unsupported"
	reasonPermission  = "permission"
	reasonError       = "error"
)

func newSkippedCounter(ts component.TelemetrySettings) (metric.Int64Counter, error) {
	return ts.MeterProvider.Meter(scopeName).Int64Counter(skippedMetric,
		metric.WithDescription("Disks, or steps of a disk, the windiskhealth receiver skipped in one interval, by reason."),
		metric.WithUnit("{device}"))
}
