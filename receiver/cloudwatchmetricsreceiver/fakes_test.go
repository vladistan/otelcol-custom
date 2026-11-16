// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package cloudwatchmetricsreceiver

import (
	"context"
	"strconv"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
)

type listCall struct{ namespace, name string }

type dataCall struct {
	queries int
	start   time.Time
	end     time.Time
	period  int32
	stats   map[string]int
}

// fakeAPI answers ListMetrics from a fixed table and GetMetricData with one point per query.
type fakeAPI struct {
	mu        sync.Mutex
	metrics   map[string][]types.Metric // key: namespace|name; name is empty for a namespace-only call
	listPage  int
	dataPage  int
	listErr   error
	dataErr   error
	noData    func(stat, namespace, name string) bool
	listCalls []listCall
	dataCalls []dataCall
}

func newFakeAPI() *fakeAPI { return &fakeAPI{metrics: map[string][]types.Metric{}} }

func (f *fakeAPI) add(namespace, name string, dims ...string) *fakeAPI {
	m := types.Metric{Namespace: aws.String(namespace), MetricName: aws.String(name)}
	for i := 0; i+1 < len(dims); i += 2 {
		m.Dimensions = append(m.Dimensions, types.Dimension{Name: aws.String(dims[i]), Value: aws.String(dims[i+1])})
	}
	f.metrics[namespace+"|"+name] = append(f.metrics[namespace+"|"+name], m)
	f.metrics[namespace+"|"] = append(f.metrics[namespace+"|"], m)
	return f
}

func (f *fakeAPI) ListMetrics(_ context.Context, in *cloudwatch.ListMetricsInput, _ ...func(*cloudwatch.Options)) (*cloudwatch.ListMetricsOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listCalls = append(f.listCalls, listCall{aws.ToString(in.Namespace), aws.ToString(in.MetricName)})
	if f.listErr != nil {
		return nil, f.listErr
	}
	all := f.metrics[aws.ToString(in.Namespace)+"|"+aws.ToString(in.MetricName)]
	if f.listPage <= 0 {
		return &cloudwatch.ListMetricsOutput{Metrics: all}, nil
	}
	from := 0
	if in.NextToken != nil {
		from, _ = strconv.Atoi(*in.NextToken)
	}
	to := min(from+f.listPage, len(all))
	out := &cloudwatch.ListMetricsOutput{Metrics: all[from:to]}
	if to < len(all) {
		out.NextToken = aws.String(strconv.Itoa(to))
	}
	return out, nil
}

func (f *fakeAPI) GetMetricData(_ context.Context, in *cloudwatch.GetMetricDataInput, _ ...func(*cloudwatch.Options)) (*cloudwatch.GetMetricDataOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.dataErr != nil {
		return nil, f.dataErr
	}
	call := dataCall{queries: len(in.MetricDataQueries), start: aws.ToTime(in.StartTime), end: aws.ToTime(in.EndTime), stats: map[string]int{}}
	var results []types.MetricDataResult
	for _, q := range in.MetricDataQueries {
		stat := aws.ToString(q.MetricStat.Stat)
		call.stats[stat]++
		call.period = aws.ToInt32(q.MetricStat.Period)
		res := types.MetricDataResult{Id: q.Id}
		if f.noData == nil || !f.noData(stat, aws.ToString(q.MetricStat.Metric.Namespace), aws.ToString(q.MetricStat.Metric.MetricName)) {
			res.Timestamps = []time.Time{aws.ToTime(in.StartTime)}
			res.Values = []float64{1.5}
		}
		results = append(results, res)
	}
	f.dataCalls = append(f.dataCalls, call)
	if f.dataPage <= 0 {
		return &cloudwatch.GetMetricDataOutput{MetricDataResults: results}, nil
	}
	// split every call's results over pages; the first page of a repeat call is the same data again
	from := 0
	if in.NextToken != nil {
		from, _ = strconv.Atoi(*in.NextToken)
	}
	to := min(from+f.dataPage, len(results))
	out := &cloudwatch.GetMetricDataOutput{MetricDataResults: results[from:to]}
	if to < len(results) {
		out.NextToken = aws.String(strconv.Itoa(to))
	}
	return out, nil
}

func (f *fakeAPI) lists() []listCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]listCall(nil), f.listCalls...)
}

func (f *fakeAPI) data() []dataCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]dataCall(nil), f.dataCalls...)
}

// fakeClients hands out one fakeAPI per account and region, or an error for an account.
type fakeClients struct {
	mu    sync.Mutex
	apis  map[string]*fakeAPI
	fail  map[string]error // by account name
	calls map[string]int
	build func(account, region string) *fakeAPI
}

func newFakeClients(build func(account, region string) *fakeAPI) *fakeClients {
	return &fakeClients{apis: map[string]*fakeAPI{}, fail: map[string]error{}, calls: map[string]int{}, build: build}
}

func (f *fakeClients) client(_ context.Context, acct AccountConfig, region string) (metricsAPI, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := acct.Name + "/" + region
	f.calls[key]++
	if err := f.fail[acct.Name]; err != nil {
		return nil, err
	}
	api, ok := f.apis[key]
	if !ok {
		api = f.build(acct.Name, region)
		f.apis[key] = api
	}
	return api, nil
}

func (f *fakeClients) api(account, region string) *fakeAPI {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.apis[account+"/"+region]
}
