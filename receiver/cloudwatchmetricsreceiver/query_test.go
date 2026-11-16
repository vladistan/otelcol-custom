// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package cloudwatchmetricsreceiver

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
)

func seriesN(n int) []seriesRef {
	out := make([]seriesRef, n)
	for i := range out {
		out[i] = seriesRef{
			namespace: "AWS/EC2", name: "CPUUtilization",
			dims: []types.Dimension{{Name: aws.String("InstanceId"), Value: aws.String(fmt.Sprintf("i-%04d", i))}},
		}
	}
	return out
}

var testWindow = window{start: utc(14, 0, 0), end: utc(15, 0, 0)}

func TestReadWindowBatchesAtMostFiveHundredQueriesPerCall(t *testing.T) {
	api := newFakeAPI()

	_, err := readWindow(context.Background(), api, seriesN(1200), []string{"Average"}, 24*time.Hour, testWindow)

	require.NoError(t, err)
	var sizes []int
	for _, c := range api.data() {
		sizes = append(sizes, c.queries)
	}
	assert.Equal(t, []int{500, 500, 200}, sizes)
}

func TestReadWindowCountsEveryStatAsAQuery(t *testing.T) {
	api := newFakeAPI()

	_, err := readWindow(context.Background(), api, seriesN(300), []string{"Average", "Maximum"}, 15*time.Minute, testWindow)

	require.NoError(t, err)
	var sizes []int
	for _, c := range api.data() {
		sizes = append(sizes, c.queries)
	}
	assert.Equal(t, []int{500, 100}, sizes, "300 series and 2 stats make 600 queries")
}

func TestReadWindowPassesTheWindowAndPeriod(t *testing.T) {
	api := newFakeAPI()

	_, err := readWindow(context.Background(), api, seriesN(1), []string{"Sum"}, 24*time.Hour, testWindow)

	require.NoError(t, err)
	call := api.data()[0]
	assert.True(t, testWindow.start.Equal(call.start))
	assert.True(t, testWindow.end.Equal(call.end))
	assert.Equal(t, int32(86400), call.period)
}

func TestReadWindowFollowsResultPagination(t *testing.T) {
	api := newFakeAPI()
	api.dataPage = 7

	got, err := readWindow(context.Background(), api, seriesN(20), []string{"Average"}, time.Hour, testWindow)

	require.NoError(t, err)
	assert.Len(t, got, 20)
	for i, sv := range got {
		assert.Len(t, sv.byStat["Average"], 1, "series %d", i)
	}
}

func TestReadWindowReturnsTheGetMetricDataError(t *testing.T) {
	api := newFakeAPI()
	api.dataErr = errors.New("access denied")

	_, err := readWindow(context.Background(), api, seriesN(2), []string{"Sum"}, time.Hour, testWindow)

	assert.ErrorContains(t, err, "access denied")
}

func TestReadWindowSeriesWithoutValuesHaveNoPoints(t *testing.T) {
	api := newFakeAPI()
	api.noData = func(stat, _, _ string) bool { return stat == "p50" }

	got, err := readWindow(context.Background(), api, seriesN(1), []string{"Average", "p50"}, time.Hour, testWindow)

	require.NoError(t, err)
	assert.Len(t, got[0].byStat["Average"], 1)
	assert.Empty(t, got[0].byStat["p50"])
}

func gaugePoints(t *testing.T, md pmetric.Metrics) (resource pcommon.Map, metrics map[string][]pmetric.NumberDataPoint) {
	t.Helper()
	require.Equal(t, 1, md.ResourceMetrics().Len())
	rm := md.ResourceMetrics().At(0)
	metrics = map[string][]pmetric.NumberDataPoint{}
	ms := rm.ScopeMetrics().At(0).Metrics()
	for i := 0; i < ms.Len(); i++ {
		require.Equal(t, pmetric.MetricTypeGauge, ms.At(i).Type(), ms.At(i).Name())
		dps := ms.At(i).Gauge().DataPoints()
		for j := 0; j < dps.Len(); j++ {
			metrics[ms.At(i).Name()] = append(metrics[ms.At(i).Name()], dps.At(j))
		}
	}
	return rm.Resource().Attributes(), metrics
}

func values(ts time.Time, v float64) []statPoint { return []statPoint{{ts: ts, value: v}} }

func TestBuildMetricsOneGaugePointPerStatOnTheAmazonawsName(t *testing.T) {
	series := seriesN(1)
	vals := []seriesValues{{byStat: map[string][]statPoint{"Average": values(testWindow.start, 1.5), "Maximum": values(testWindow.start, 9)}}}

	md := buildMetrics("0.3.15", "prod", "us-east-1", series, vals, []string{"Average", "Maximum"})

	res, metrics := gaugePoints(t, md)
	require.Contains(t, metrics, "amazonaws.com/AWS/EC2/CPUUtilization")
	points := metrics["amazonaws.com/AWS/EC2/CPUUtilization"]
	require.Len(t, points, 2)
	byStat := map[string]float64{}
	for _, dp := range points {
		stat, _ := dp.Attributes().Get("stat")
		byStat[stat.Str()] = dp.DoubleValue()
	}
	assert.Equal(t, map[string]float64{"Average": 1.5, "Maximum": 9}, byStat)
	for _, key := range []string{"cloud.provider", "cloud.region", "cloud.account.name"} {
		_, ok := res.Get(key)
		assert.True(t, ok, key)
	}
}

func TestBuildMetricsResourceAttributes(t *testing.T) {
	md := buildMetrics("0.3.15", "prod", "eu-west-1", seriesN(1),
		[]seriesValues{{byStat: map[string][]statPoint{"Sum": values(testWindow.start, 1)}}}, []string{"Sum"})

	res, _ := gaugePoints(t, md)

	assert.Equal(t, map[string]any{"cloud.provider": "aws", "cloud.region": "eu-west-1", "cloud.account.name": "prod"}, res.AsRaw())
}

func TestBuildMetricsPointCarriesNamespaceNameAndItsOwnDimensions(t *testing.T) {
	series := seriesN(2)
	vals := []seriesValues{
		{byStat: map[string][]statPoint{"Sum": values(testWindow.start, 1)}},
		{byStat: map[string][]statPoint{"Sum": values(testWindow.start, 2)}},
	}

	md := buildMetrics("0.3.15", "prod", "us-east-1", series, vals, []string{"Sum"})

	_, metrics := gaugePoints(t, md)
	points := metrics["amazonaws.com/AWS/EC2/CPUUtilization"]
	require.Len(t, points, 2)
	seen := map[string]float64{}
	for _, dp := range points {
		ns, _ := dp.Attributes().Get("Namespace")
		name, _ := dp.Attributes().Get("MetricName")
		dims, _ := dp.Attributes().Get("Dimensions")
		id, _ := dims.Map().Get("InstanceId")
		assert.Equal(t, "AWS/EC2", ns.Str())
		assert.Equal(t, "CPUUtilization", name.Str())
		seen[id.Str()] = dp.DoubleValue()
	}
	assert.Equal(t, map[string]float64{"i-0000": 1, "i-0001": 2}, seen)
}

func TestBuildMetricsPointTimestampIsTheDatapointTime(t *testing.T) {
	md := buildMetrics("0.3.15", "prod", "us-east-1", seriesN(1),
		[]seriesValues{{byStat: map[string][]statPoint{"Sum": values(testWindow.start, 1)}}}, []string{"Sum"})

	_, metrics := gaugePoints(t, md)

	assert.Equal(t, pcommon.NewTimestampFromTime(testWindow.start), metrics["amazonaws.com/AWS/EC2/CPUUtilization"][0].Timestamp())
}

func TestBuildMetricsWithoutAnyValueIsEmpty(t *testing.T) {
	md := buildMetrics("0.3.15", "prod", "us-east-1", seriesN(2), []seriesValues{{}, {}}, []string{"Sum"})

	assert.Equal(t, 0, md.ResourceMetrics().Len())
}

func TestBuildMetricsOneMetricPerNamespaceAndName(t *testing.T) {
	series := []seriesRef{
		{namespace: "AWS/EC2", name: "CPUUtilization"},
		{namespace: "AWS/EC2", name: "NetworkIn"},
		{namespace: "AWS/RDS", name: "CPUUtilization"},
	}
	vals := make([]seriesValues, 3)
	for i := range vals {
		vals[i] = seriesValues{byStat: map[string][]statPoint{"Sum": values(testWindow.start, float64(i))}}
	}

	md := buildMetrics("0.3.15", "prod", "us-east-1", series, vals, []string{"Sum"})

	_, metrics := gaugePoints(t, md)
	assert.Len(t, metrics, 3)
}

func TestMissingPercentilesNamesTheMetricsWhereOnlyAPercentileIsEmpty(t *testing.T) {
	series := []seriesRef{
		{namespace: "AWS/EC2", name: "CPUUtilization"},
		{namespace: "AWS/Lambda", name: "Duration"},
		{namespace: "AWS/RDS", name: "Idle"},
	}
	vals := []seriesValues{
		{byStat: map[string][]statPoint{"Average": values(testWindow.start, 1)}},
		{byStat: map[string][]statPoint{"Average": values(testWindow.start, 1), "p50": values(testWindow.start, 1)}},
		{byStat: map[string][]statPoint{}},
	}

	got := missingPercentiles(series, vals, []string{"Average", "p50"})

	assert.Equal(t, []string{"AWS/EC2/CPUUtilization"}, got, "a metric with no data at all is not a percentile gap")
}

func TestMissingPercentilesIgnoresTiersWithoutAPercentile(t *testing.T) {
	series := seriesN(1)
	vals := []seriesValues{{byStat: map[string][]statPoint{"Average": values(testWindow.start, 1)}}}

	assert.Empty(t, missingPercentiles(series, vals, []string{"Average", "Maximum"}))
}
