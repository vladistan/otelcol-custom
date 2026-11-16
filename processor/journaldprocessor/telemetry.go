// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package journaldprocessor

import (
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/otel/metric"
)

const (
	scopeName               = "github.com/vladistan/otelcol-custom/processor/journaldprocessor"
	resourcesPerBatchMetric = "otelcol_journald_resources_per_batch"
)

// newResourcesPerBatch makes the histogram of resources in each batch the processor outputs.
func newResourcesPerBatch(ts component.TelemetrySettings) (metric.Int64Histogram, error) {
	return ts.MeterProvider.Meter(scopeName).Int64Histogram(resourcesPerBatchMetric,
		metric.WithDescription("Resources in each batch the processor passes on. Splitting on extended fields can make many."),
		metric.WithUnit("{resource}"),
		metric.WithExplicitBucketBoundaries(1, 2, 5, 10, 20, 50, 100, 200, 500, 1000, 2000, 5000))
}
