// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package cloudwatchmetricsreceiver

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
)

// maxQueriesPerCall is the GetMetricData limit on queries in one request.
const maxQueriesPerCall = 500

var percentileStat = regexp.MustCompile(`^p\d+(\.\d+)?$`)

type statPoint struct {
	ts    time.Time
	value float64
}

// seriesValues holds the points read for one series, by statistic name.
type seriesValues struct{ byStat map[string][]statPoint }

type queryIndex struct{ series, stat int }

// readWindow reads every statistic of every series for one window. Each statistic
// of a series is one query; queries go out in calls of at most maxQueriesPerCall.
func readWindow(ctx context.Context, api metricsAPI, series []seriesRef, stats []string, period time.Duration, w window) ([]seriesValues, error) {
	out := make([]seriesValues, len(series))
	for i := range out {
		out[i].byStat = map[string][]statPoint{}
	}
	var batch []types.MetricDataQuery
	index := map[string]queryIndex{}
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		err := runBatch(ctx, api, batch, index, stats, w, out)
		batch, index = nil, map[string]queryIndex{}
		return err
	}
	for si, s := range series {
		for ti, stat := range stats {
			id := fmt.Sprintf("q%d_%d", si, ti)
			index[id] = queryIndex{si, ti}
			batch = append(batch, types.MetricDataQuery{
				Id: aws.String(id),
				MetricStat: &types.MetricStat{
					Metric: &types.Metric{Namespace: aws.String(s.namespace), MetricName: aws.String(s.name), Dimensions: s.dims},
					Period: aws.Int32(int32(period / time.Second)),
					Stat:   aws.String(stat),
				},
			})
			if len(batch) == maxQueriesPerCall {
				if err := flush(); err != nil {
					return nil, err
				}
			}
		}
	}
	if err := flush(); err != nil {
		return nil, err
	}
	for _, sv := range out {
		for _, points := range sv.byStat {
			slices.SortFunc(points, func(a, b statPoint) int { return a.ts.Compare(b.ts) })
		}
	}
	return out, nil
}

func runBatch(ctx context.Context, api metricsAPI, batch []types.MetricDataQuery, index map[string]queryIndex, stats []string, w window, out []seriesValues) error {
	in := &cloudwatch.GetMetricDataInput{
		StartTime: aws.Time(w.start), EndTime: aws.Time(w.end), MetricDataQueries: batch, ScanBy: types.ScanByTimestampAscending,
	}
	for {
		resp, err := api.GetMetricData(ctx, in)
		if err != nil {
			return fmt.Errorf("GetMetricData: %w", err)
		}
		for _, r := range resp.MetricDataResults {
			at, ok := index[aws.ToString(r.Id)]
			if !ok {
				continue
			}
			stat := stats[at.stat]
			for i := 0; i < min(len(r.Values), len(r.Timestamps)); i++ {
				out[at.series].byStat[stat] = append(out[at.series].byStat[stat], statPoint{ts: r.Timestamps[i], value: r.Values[i]})
			}
		}
		if resp.NextToken == nil {
			return nil
		}
		in.NextToken = resp.NextToken
	}
}

// buildMetrics turns the points of one window into gauges, one metric per
// namespace and name and one point per series, statistic and time. It returns an
// empty Metrics when no series has a point.
func buildMetrics(version, account, region string, series []seriesRef, vals []seriesValues, stats []string) pmetric.Metrics {
	md := pmetric.NewMetrics()
	rm := md.ResourceMetrics().AppendEmpty()
	attrs := rm.Resource().Attributes()
	attrs.PutStr("cloud.provider", "aws")
	attrs.PutStr("cloud.region", region)
	attrs.PutStr("cloud.account.name", account)
	sm := rm.ScopeMetrics().AppendEmpty()
	sm.Scope().SetName(scopeName)
	sm.Scope().SetVersion(version)

	gauges := map[string]pmetric.NumberDataPointSlice{}
	for si, s := range series {
		name := "amazonaws.com/" + s.namespace + "/" + s.name
		for _, stat := range stats {
			for _, p := range vals[si].byStat[stat] {
				dps, ok := gauges[name]
				if !ok {
					m := sm.Metrics().AppendEmpty()
					m.SetName(name)
					dps = m.SetEmptyGauge().DataPoints()
					gauges[name] = dps
				}
				dp := dps.AppendEmpty()
				dp.SetTimestamp(pcommon.NewTimestampFromTime(p.ts))
				dp.SetDoubleValue(p.value)
				setSeriesAttrs(dp.Attributes(), s, stat)
			}
		}
	}
	if sm.Metrics().Len() == 0 {
		return pmetric.NewMetrics()
	}
	return md
}

func setSeriesAttrs(attrs pcommon.Map, s seriesRef, stat string) {
	attrs.PutStr("Namespace", s.namespace)
	attrs.PutStr("MetricName", s.name)
	if len(s.dims) > 0 {
		dims := attrs.PutEmptyMap("Dimensions")
		for _, d := range s.dims {
			dims.PutStr(aws.ToString(d.Name), aws.ToString(d.Value))
		}
	}
	attrs.PutStr("stat", stat)
}

// missingPercentiles names the metrics that returned data for a plain statistic
// but none for a percentile one, which is how CloudWatch answers a percentile
// asked of a metric without percentiles. Each name is listed once.
func missingPercentiles(series []seriesRef, vals []seriesValues, stats []string) []string {
	var out []string
	for si, s := range series {
		hasPlain, hasGap := false, false
		for _, stat := range stats {
			got := len(vals[si].byStat[stat]) > 0
			switch {
			case percentileStat.MatchString(stat) && !got:
				hasGap = true
			case !percentileStat.MatchString(stat) && got:
				hasPlain = true
			}
		}
		name := s.namespace + "/" + s.name
		if hasPlain && hasGap && !slices.Contains(out, name) {
			out = append(out, name)
		}
	}
	return out
}
