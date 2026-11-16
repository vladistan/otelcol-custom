// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package smartreceiver

import (
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/otel/metric"
)

const (
	scopeName      = "github.com/vladistan/otelcol-custom/receiver/smartreceiver"
	skippedMetric  = "otelcol_smart_devices_skipped"
	reasonLabelKey = "reason"
)

// Reasons a device is skipped in one interval.
const (
	reasonTimeout       = "timeout"
	reasonUnsupported   = "unsupported"
	reasonSmartDisabled = "smart_disabled"
	reasonStandby       = "standby"
	reasonPermission    = "permission"
	reasonError         = "error"
)

// newSkippedCounter makes the counter of devices skipped, by reason.
func newSkippedCounter(ts component.TelemetrySettings) (metric.Int64Counter, error) {
	return ts.MeterProvider.Meter(scopeName).Int64Counter(skippedMetric,
		metric.WithDescription("Devices the smart receiver skipped in one interval, by reason."),
		metric.WithUnit("{device}"))
}
