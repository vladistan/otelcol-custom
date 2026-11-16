// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package cloudwatchmetricsreceiver

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDiscoverRunsOneListMetricsCallPerListedName(t *testing.T) {
	api := newFakeAPI().
		add("AWS/EC2", "CPUUtilization", "InstanceId", "i-1").
		add("AWS/EC2", "NetworkIn", "InstanceId", "i-1").
		add("AWS/EC2", "DiskReadOps", "InstanceId", "i-1")

	got, err := discoverSeries(context.Background(), api, map[string][]string{"AWS/EC2": {"CPUUtilization", "NetworkIn", "DiskReadOps"}}, 100)

	require.NoError(t, err)
	assert.Equal(t, []listCall{{"AWS/EC2", "CPUUtilization"}, {"AWS/EC2", "NetworkIn"}, {"AWS/EC2", "DiskReadOps"}}, api.lists())
	assert.Len(t, got.series, 3)
}

func TestDiscoverEmptyNameListRunsOneCallWithTheNamespaceOnly(t *testing.T) {
	api := newFakeAPI().
		add("AWS/RDS", "CPUUtilization", "DBInstanceIdentifier", "db-1").
		add("AWS/RDS", "FreeStorageSpace", "DBInstanceIdentifier", "db-1")

	got, err := discoverSeries(context.Background(), api, map[string][]string{"AWS/RDS": nil}, 100)

	require.NoError(t, err)
	assert.Equal(t, []listCall{{"AWS/RDS", ""}}, api.lists())
	assert.Len(t, got.series, 2)
}

func TestDiscoverKeepsEachDimensionSetAsItsOwnSeries(t *testing.T) {
	api := newFakeAPI().
		add("AWS/EC2", "CPUUtilization", "InstanceId", "i-1").
		add("AWS/EC2", "CPUUtilization", "InstanceId", "i-2")

	got, err := discoverSeries(context.Background(), api, map[string][]string{"AWS/EC2": {"CPUUtilization"}}, 100)

	require.NoError(t, err)
	require.Len(t, got.series, 2)
	assert.Equal(t, "i-1", aws.ToString(got.series[0].dims[0].Value))
	assert.Equal(t, "i-2", aws.ToString(got.series[1].dims[0].Value))
}

func TestDiscoverNamespacesAreVisitedInSortedOrder(t *testing.T) {
	api := newFakeAPI().add("AWS/RDS", "A").add("AWS/EC2", "B").add("AWS/Lambda", "C")

	_, err := discoverSeries(context.Background(), api, map[string][]string{"AWS/RDS": {"A"}, "AWS/Lambda": {"C"}, "AWS/EC2": {"B"}}, 100)

	require.NoError(t, err)
	assert.Equal(t, []listCall{{"AWS/EC2", "B"}, {"AWS/Lambda", "C"}, {"AWS/RDS", "A"}}, api.lists())
}

func TestDiscoverNoMatchGivesNothingAndNoError(t *testing.T) {
	got, err := discoverSeries(context.Background(), newFakeAPI(), map[string][]string{"AWS/EC2": {"Nothing"}}, 10)

	require.NoError(t, err)
	assert.Empty(t, got.series)
	assert.Zero(t, got.total)
	assert.False(t, got.truncated())
}

func twelveMetrics() *fakeAPI {
	api := newFakeAPI()
	for i := 0; i < 12; i++ {
		api.add("AWS/EC2", "CPUUtilization", "InstanceId", fmt.Sprintf("i-%02d", i))
	}
	return api
}

func TestDiscoverBeyondTheLimitCountsAllButKeepsOnlyTheLimit(t *testing.T) {
	got, err := discoverSeries(context.Background(), twelveMetrics(), map[string][]string{"AWS/EC2": {"CPUUtilization"}}, 10)

	require.NoError(t, err)
	assert.Equal(t, 12, got.total, "paging goes on past the limit so the whole total is known")
	assert.Len(t, got.series, 10)
	assert.True(t, got.truncated())
	assert.Equal(t, "i-09", aws.ToString(got.series[9].dims[0].Value), "the first ten in discovery order")
}

func TestDiscoverFollowsPagination(t *testing.T) {
	api := twelveMetrics()
	api.listPage = 5

	got, err := discoverSeries(context.Background(), api, map[string][]string{"AWS/EC2": {"CPUUtilization"}}, 100)

	require.NoError(t, err)
	assert.Len(t, got.series, 12)
	assert.Len(t, api.lists(), 3, "pages of 5, 5 and 2")
}

func TestDiscoverTotalCountsEveryNameAgainstOneLimit(t *testing.T) {
	api := newFakeAPI().
		add("AWS/EC2", "A", "k", "1").add("AWS/EC2", "A", "k", "2").
		add("AWS/EC2", "B", "k", "1").add("AWS/EC2", "B", "k", "2")

	got, err := discoverSeries(context.Background(), api, map[string][]string{"AWS/EC2": {"A", "B"}}, 3)

	require.NoError(t, err)
	assert.Equal(t, 4, got.total)
	assert.Len(t, got.series, 3)
}

func TestDiscoverReturnsTheListMetricsError(t *testing.T) {
	api := newFakeAPI()
	api.listErr = errors.New("throttled")

	_, err := discoverSeries(context.Background(), api, map[string][]string{"AWS/EC2": {"A"}}, 10)

	assert.ErrorContains(t, err, "throttled")
}
