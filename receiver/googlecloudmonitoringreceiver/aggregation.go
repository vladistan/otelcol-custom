// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package googlecloudmonitoringreceiver

import (
	"context"
	"errors"
	"fmt"
	"time"

	"cloud.google.com/go/monitoring/apiv3/v2/monitoringpb"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.uber.org/zap"
	"google.golang.org/api/iterator"
	"google.golang.org/genproto/googleapis/api/metric"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/googlecloudmonitoringreceiver/internal"
)

const (
	// maxQueryWindow is the longest window of a request without aggregation.
	maxQueryWindow = 23 * time.Hour
	// minAlignmentPeriod is the shortest alignment period Cloud Monitoring accepts.
	minAlignmentPeriod = time.Minute
	statAttribute      = "stat"
	percentileAligner  = "ALIGN_PERCENTILE_50"
)

// alignerStats maps the accepted aligners to the Aggregation enum and the value of the stat attribute.
var alignerStats = map[string]struct {
	enum monitoringpb.Aggregation_Aligner
	stat string
}{
	"ALIGN_MIN":       {monitoringpb.Aggregation_ALIGN_MIN, "min"},
	"ALIGN_MAX":       {monitoringpb.Aggregation_ALIGN_MAX, "max"},
	"ALIGN_MEAN":      {monitoringpb.Aggregation_ALIGN_MEAN, "mean"},
	"ALIGN_SUM":       {monitoringpb.Aggregation_ALIGN_SUM, "sum"},
	"ALIGN_COUNT":     {monitoringpb.Aggregation_ALIGN_COUNT, "count"},
	percentileAligner: {monitoringpb.Aggregation_ALIGN_PERCENTILE_50, "p50"},
}

// AggregationConfig sets per-series aligned requests: one ListTimeSeries request per metric type, project and aligner.
type AggregationConfig struct {
	// AlignmentPeriod is the width of the buckets the aligner works over; one day gives one point per series.
	AlignmentPeriod time.Duration `mapstructure:"alignment_period"`
	// PerSeriesAligners lists ALIGN_MIN, ALIGN_MAX, ALIGN_MEAN, ALIGN_SUM, ALIGN_COUNT and
	// ALIGN_PERCENTILE_50 (distribution metrics only).
	PerSeriesAligners []string `mapstructure:"per_series_aligners"`
}

// Validate rejects a short alignment period, an empty list and any aligner outside the accepted list.
func (a *AggregationConfig) Validate() error {
	if a.AlignmentPeriod < minAlignmentPeriod {
		return fmt.Errorf("\"aggregation.alignment_period\" must be at least %v, current value is %v", minAlignmentPeriod, a.AlignmentPeriod)
	}
	if len(a.PerSeriesAligners) == 0 {
		return errors.New("missing required field \"aggregation.per_series_aligners\" or its value is empty")
	}
	seen := map[string]bool{}
	for _, aligner := range a.PerSeriesAligners {
		if _, ok := alignerStats[aligner]; !ok {
			return fmt.Errorf("\"aggregation.per_series_aligners\" has the unsupported aligner %q", aligner)
		}
		if seen[aligner] {
			return fmt.Errorf("\"aggregation.per_series_aligners\" lists %s more than once", aligner)
		}
		seen[aligner] = true
	}
	return nil
}

// statName returns the stat attribute value of an aligner.
func statName(aligner string) (string, bool) {
	entry, ok := alignerStats[aligner]
	return entry.stat, ok
}

// scrapeAggregated sends one request per aligner for one metric type in one project and appends the results.
// A request the API rejects is logged and the next one goes on. missingPercentile holds the metric names
// that already logged the missing-percentile line in this run.
func (mr *monitoringReceiver) scrapeAggregated(
	ctx context.Context,
	metrics pmetric.Metrics,
	project, metricType string,
	desc *metric.MetricDescriptor,
	missingPercentile map[string]bool,
) {
	interval := mr.config.ControllerConfig.CollectionInterval
	if interval <= 0 {
		interval = defaultCollectionInterval
	}
	delay := desc.GetMetadata().GetIngestDelay().AsDuration()
	if delay <= 0 {
		delay = defaultFetchDelay
	}
	endTime := time.Now().Add(-delay)
	startTime := endTime.Add(-interval)

	for _, aligner := range mr.config.Aggregation.PerSeriesAligners {
		entry := alignerStats[aligner]
		if aligner == percentileAligner && desc.GetValueType() != metric.MetricDescriptor_DISTRIBUTION {
			if !missingPercentile[metricType] {
				missingPercentile[metricType] = true
				mr.logger.Info("skipping the percentile statistic: the metric is not a distribution",
					zap.String("metric", metricType))
			}
			continue
		}

		req := &monitoringpb.ListTimeSeriesRequest{
			Name:   "projects/" + project,
			Filter: fmt.Sprintf(`metric.type = %q`, metricType),
			Interval: &monitoringpb.TimeInterval{
				EndTime:   &timestamppb.Timestamp{Seconds: endTime.Unix()},
				StartTime: &timestamppb.Timestamp{Seconds: startTime.Unix()},
			},
			Aggregation: &monitoringpb.Aggregation{
				AlignmentPeriod:  durationpb.New(mr.config.Aggregation.AlignmentPeriod),
				PerSeriesAligner: entry.enum,
			},
			View: monitoringpb.ListTimeSeriesRequest_FULL,
		}

		tsIter := mr.client.ListTimeSeries(ctx, req)
		for {
			timeSeries, err := tsIter.Next()
			if errors.Is(err, iterator.Done) {
				break
			}
			if err != nil {
				mr.logger.Warn("aligned request failed; the other requests continue",
					zap.String("project", project), zap.String("metric", metricType),
					zap.String("aligner", aligner), zap.Error(err))
				break
			}
			mr.convertAlignedSeries(metrics, desc, timeSeries, entry.stat)
		}
	}
}

// convertAlignedSeries appends one aligned series as a gauge whose data points carry the stat attribute.
func (mr *monitoringReceiver) convertAlignedSeries(metrics pmetric.Metrics, desc *metric.MetricDescriptor, timeSeries *monitoringpb.TimeSeries, stat string) {
	if timeSeries == nil {
		return
	}
	internal.SortPointsChronologically(timeSeries.Points)

	rm := newSeriesResource(metrics, timeSeries)
	m := rm.ScopeMetrics().At(0).Metrics().AppendEmpty()
	m.SetName(desc.GetName())
	m.SetDescription(desc.GetDescription())
	m.SetUnit(desc.GetUnit())
	if stat == "count" {
		m.SetUnit("1")
	}

	dps := m.SetEmptyGauge().DataPoints()
	for _, point := range timeSeries.GetPoints() {
		var set func(pmetric.NumberDataPoint)
		switch v := point.GetValue().GetValue().(type) {
		case *monitoringpb.TypedValue_Int64Value:
			set = func(dp pmetric.NumberDataPoint) { dp.SetIntValue(v.Int64Value) }
		case *monitoringpb.TypedValue_DoubleValue:
			set = func(dp pmetric.NumberDataPoint) { dp.SetDoubleValue(v.DoubleValue) }
		default:
			mr.logger.Debug("aligned point has a value type the receiver does not convert",
				zap.String("metric", desc.GetType()), zap.String("stat", stat))
			continue
		}
		dp := dps.AppendEmpty()
		set(dp)
		for k, v := range timeSeries.GetMetric().GetLabels() {
			dp.Attributes().PutStr(k, v)
		}
		dp.Attributes().PutStr(statAttribute, stat)
		dp.SetStartTimestamp(pcommon.NewTimestampFromTime(point.GetInterval().GetStartTime().AsTime()))
		dp.SetTimestamp(pcommon.NewTimestampFromTime(point.GetInterval().GetEndTime().AsTime()))
	}
}
