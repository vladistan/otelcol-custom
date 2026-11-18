// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package googlecloudmonitoringreceiver

import (
	"context"
	"net"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	monitoring "cloud.google.com/go/monitoring/apiv3/v2"
	"cloud.google.com/go/monitoring/apiv3/v2/monitoringpb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
	"google.golang.org/api/option"
	"google.golang.org/genproto/googleapis/api/metric"
	"google.golang.org/genproto/googleapis/api/monitoredres"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

// fakeMonitoring is an in-process Cloud Monitoring server. It records every ListTimeSeries request.
type fakeMonitoring struct {
	monitoringpb.UnimplementedMetricServiceServer

	mu          sync.Mutex
	descriptors map[string]*metric.MetricDescriptor
	// projectDescriptors, when set, answers each project with its own descriptors and nothing else.
	projectDescriptors map[string]map[string]*metric.MetricDescriptor
	requests           []*monitoringpb.ListTimeSeriesRequest
	descriptorProjects []string
	// failDescriptors returns an error for a descriptor request, or nil.
	failDescriptors func(*monitoringpb.ListMetricDescriptorsRequest) error
	// failFor returns an error for a request, or nil.
	failFor func(*monitoringpb.ListTimeSeriesRequest) error
}

func metricTypeOf(filter string) string {
	start := strings.Index(filter, `"`)
	end := strings.LastIndex(filter, `"`)
	if start < 0 || end <= start {
		return ""
	}
	return filter[start+1 : end]
}

func (f *fakeMonitoring) ListMetricDescriptors(_ context.Context, req *monitoringpb.ListMetricDescriptorsRequest) (*monitoringpb.ListMetricDescriptorsResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.descriptorProjects = append(f.descriptorProjects, req.GetName())
	if f.failDescriptors != nil {
		if err := f.failDescriptors(req); err != nil {
			return nil, err
		}
	}
	resp := &monitoringpb.ListMetricDescriptorsResponse{}
	source := f.descriptors
	if f.projectDescriptors != nil {
		source = f.projectDescriptors[strings.TrimPrefix(req.GetName(), "projects/")]
	}
	if desc, ok := source[metricTypeOf(req.GetFilter())]; ok {
		resp.MetricDescriptors = append(resp.MetricDescriptors, desc)
	}
	return resp, nil
}

func alignedValue(aligner monitoringpb.Aggregation_Aligner) *monitoringpb.TypedValue {
	switch aligner {
	case monitoringpb.Aggregation_ALIGN_MIN:
		return &monitoringpb.TypedValue{Value: &monitoringpb.TypedValue_DoubleValue{DoubleValue: 1}}
	case monitoringpb.Aggregation_ALIGN_MAX:
		return &monitoringpb.TypedValue{Value: &monitoringpb.TypedValue_DoubleValue{DoubleValue: 5}}
	case monitoringpb.Aggregation_ALIGN_MEAN:
		return &monitoringpb.TypedValue{Value: &monitoringpb.TypedValue_DoubleValue{DoubleValue: 3}}
	case monitoringpb.Aggregation_ALIGN_SUM:
		return &monitoringpb.TypedValue{Value: &monitoringpb.TypedValue_DoubleValue{DoubleValue: 9}}
	case monitoringpb.Aggregation_ALIGN_COUNT:
		return &monitoringpb.TypedValue{Value: &monitoringpb.TypedValue_Int64Value{Int64Value: 3}}
	default:
		return &monitoringpb.TypedValue{Value: &monitoringpb.TypedValue_DoubleValue{DoubleValue: 2.5}}
	}
}

func (f *fakeMonitoring) ListTimeSeries(_ context.Context, req *monitoringpb.ListTimeSeriesRequest) (*monitoringpb.ListTimeSeriesResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, req)
	if f.failFor != nil {
		if err := f.failFor(req); err != nil {
			return nil, err
		}
	}
	aligner := req.GetAggregation().GetPerSeriesAligner()
	end := req.GetInterval().GetEndTime()
	start := req.GetInterval().GetStartTime()
	point := &monitoringpb.Point{
		Interval: &monitoringpb.TimeInterval{StartTime: start, EndTime: end},
		Value:    alignedValue(aligner),
	}
	if req.GetAggregation() == nil {
		point.Value = &monitoringpb.TypedValue{Value: &monitoringpb.TypedValue_DoubleValue{DoubleValue: 0.7}}
	}
	return &monitoringpb.ListTimeSeriesResponse{TimeSeries: []*monitoringpb.TimeSeries{{
		Metric:     &metric.Metric{Type: metricTypeOf(req.GetFilter()), Labels: map[string]string{"state": "busy"}},
		Resource:   &monitoredres.MonitoredResource{Type: "cloud_run_revision", Labels: map[string]string{"project_id": strings.TrimPrefix(req.GetName(), "projects/")}},
		MetricKind: metric.MetricDescriptor_GAUGE,
		ValueType:  metric.MetricDescriptor_DOUBLE,
		Points:     []*monitoringpb.Point{point},
	}}}, nil
}

func (f *fakeMonitoring) recorded() []*monitoringpb.ListTimeSeriesRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*monitoringpb.ListTimeSeriesRequest(nil), f.requests...)
}

func gaugeDescriptor(metricType string) *metric.MetricDescriptor {
	return &metric.MetricDescriptor{
		Name: metricType, Type: metricType, Unit: "1",
		MetricKind: metric.MetricDescriptor_GAUGE, ValueType: metric.MetricDescriptor_DOUBLE,
	}
}

func distributionDescriptor(metricType string) *metric.MetricDescriptor {
	return &metric.MetricDescriptor{
		Name: metricType, Type: metricType, Unit: "ms",
		MetricKind: metric.MetricDescriptor_DELTA, ValueType: metric.MetricDescriptor_DISTRIBUTION,
	}
}

type harness struct {
	fake *fakeMonitoring
	mr   *monitoringReceiver
	logs *observer.ObservedLogs
}

func newHarness(t *testing.T, cfg *Config, descriptors ...*metric.MetricDescriptor) *harness {
	t.Helper()
	h, err := newHarnessWith(t, cfg, nil, descriptors...)
	require.NoError(t, err)
	return h
}

// newHarnessWith runs setup on the fake before Start and returns the error of Start.
func newHarnessWith(t *testing.T, cfg *Config, setup func(*fakeMonitoring), descriptors ...*metric.MetricDescriptor) (*harness, error) {
	t.Helper()
	fake := &fakeMonitoring{descriptors: map[string]*metric.MetricDescriptor{}}
	for _, d := range descriptors {
		fake.descriptors[d.Type] = d
	}
	if setup != nil {
		setup(fake)
	}
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	monitoringpb.RegisterMetricServiceServer(server, fake)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	client, err := monitoring.NewMetricClient(context.Background(), option.WithGRPCConn(conn), option.WithoutAuthentication())
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	core, logs := observer.New(zapcore.DebugLevel)
	mr := newGoogleCloudMonitoringReceiver(cfg, zap.New(core))
	mr.client = client
	return &harness{fake: fake, mr: mr, logs: logs}, mr.Start(context.Background(), nil)
}

func metricConfigs(types ...string) []MetricConfig {
	var out []MetricConfig
	for _, typ := range types {
		out = append(out, MetricConfig{MetricName: typ})
	}
	return out
}

func aggregationConfig(projects []string, aligners []string, types ...string) *Config {
	cfg := baseConfig()
	cfg.Projects = projects
	cfg.MetricsList = metricConfigs(types...)
	cfg.Aggregation.PerSeriesAligners = aligners
	return cfg
}

func TestTwoMetricsAndThreeAlignersSendSixRequests(t *testing.T) {
	cfg := aggregationConfig([]string{"proj-a"}, []string{"ALIGN_MIN", "ALIGN_MAX", "ALIGN_MEAN"}, "m.googleapis.com/a", "m.googleapis.com/b")
	h := newHarness(t, cfg, gaugeDescriptor("m.googleapis.com/a"), gaugeDescriptor("m.googleapis.com/b"))

	_, err := h.mr.Scrape(context.Background())

	require.NoError(t, err)
	requests := h.fake.recorded()
	require.Len(t, requests, 6)
	seen := map[string]bool{}
	for _, req := range requests {
		seen[metricTypeOf(req.GetFilter())+"|"+req.GetAggregation().GetPerSeriesAligner().String()] = true
	}
	assert.Len(t, seen, 6, "one request for each metric type and aligner")
}

func TestEachRequestCarriesTheAlignmentPeriodAndTheWindow(t *testing.T) {
	cfg := aggregationConfig([]string{"proj-a"}, []string{"ALIGN_MIN", "ALIGN_MAX"}, "m.googleapis.com/a")
	cfg.Aggregation.AlignmentPeriod = 12 * time.Hour
	h := newHarness(t, cfg, gaugeDescriptor("m.googleapis.com/a"))

	_, err := h.mr.Scrape(context.Background())

	require.NoError(t, err)
	requests := h.fake.recorded()
	require.NotEmpty(t, requests, "the scrape sent no request")
	for _, req := range requests {
		assert.Equal(t, 12*time.Hour, req.GetAggregation().GetAlignmentPeriod().AsDuration())
		window := req.GetInterval().GetEndTime().AsTime().Sub(req.GetInterval().GetStartTime().AsTime())
		assert.Equal(t, 24*time.Hour, window, "a window above 23 h is sent as it is")
	}
}

func scrapedGauges(t *testing.T, metrics pmetric.Metrics) map[string]float64 {
	t.Helper()
	got := map[string]float64{}
	for i := 0; i < metrics.ResourceMetrics().Len(); i++ {
		sms := metrics.ResourceMetrics().At(i).ScopeMetrics()
		for j := 0; j < sms.Len(); j++ {
			ms := sms.At(j).Metrics()
			for k := 0; k < ms.Len(); k++ {
				m := ms.At(k)
				require.Equal(t, pmetric.MetricTypeGauge, m.Type(), "each aligned result is a gauge")
				dps := m.Gauge().DataPoints()
				for d := 0; d < dps.Len(); d++ {
					stat, ok := dps.At(d).Attributes().Get("stat")
					require.True(t, ok, "attribute stat")
					value := dps.At(d).DoubleValue()
					if dps.At(d).ValueType() == pmetric.NumberDataPointValueTypeInt {
						value = float64(dps.At(d).IntValue())
					}
					got[stat.Str()] = value
				}
			}
		}
	}
	return got
}

func TestEachAlignerGivesItsStatValue(t *testing.T) {
	aligners := []string{"ALIGN_MIN", "ALIGN_MAX", "ALIGN_MEAN", "ALIGN_SUM", "ALIGN_COUNT", "ALIGN_PERCENTILE_50"}
	cfg := aggregationConfig([]string{"proj-a"}, aligners, "m.googleapis.com/dist")
	h := newHarness(t, cfg, distributionDescriptor("m.googleapis.com/dist"))

	metrics, err := h.mr.Scrape(context.Background())

	require.NoError(t, err)
	assert.Equal(t, map[string]float64{"min": 1, "max": 5, "mean": 3, "sum": 9, "count": 3, "p50": 2.5}, scrapedGauges(t, metrics))
}

func TestAlignedPointKeepsLabelsResourceAndTime(t *testing.T) {
	cfg := aggregationConfig([]string{"proj-a"}, []string{"ALIGN_MAX"}, "m.googleapis.com/a")
	h := newHarness(t, cfg, gaugeDescriptor("m.googleapis.com/a"))

	metrics, err := h.mr.Scrape(context.Background())

	require.NoError(t, err)
	require.Equal(t, 1, metrics.ResourceMetrics().Len())
	rm := metrics.ResourceMetrics().At(0)
	resourceType, _ := rm.Resource().Attributes().Get("gcp.resource_type")
	assert.Equal(t, "cloud_run_revision", resourceType.Str())
	projectLabel, _ := rm.Resource().Attributes().Get("project_id")
	assert.Equal(t, "proj-a", projectLabel.Str())
	dp := rm.ScopeMetrics().At(0).Metrics().At(0).Gauge().DataPoints().At(0)
	state, _ := dp.Attributes().Get("state")
	assert.Equal(t, "busy", state.Str(), "the metric labels stay")
	assert.NotZero(t, dp.Timestamp())
}

func TestWithoutAggregationTheRequestAndOutputAreUnchanged(t *testing.T) {
	cfg := baseConfig()
	cfg.Aggregation = nil
	cfg.ControllerConfig.CollectionInterval = 5 * time.Minute
	cfg.MetricsList = metricConfigs("m.googleapis.com/a")
	h := newHarness(t, cfg, gaugeDescriptor("m.googleapis.com/a"))

	metrics, err := h.mr.Scrape(context.Background())

	require.NoError(t, err)
	requests := h.fake.recorded()
	require.Len(t, requests, 1)
	assert.Nil(t, requests[0].GetAggregation(), "no aggregation in the request")
	assert.Equal(t, monitoringpb.ListTimeSeriesRequest_FULL, requests[0].GetView())
	window := requests[0].GetInterval().GetEndTime().AsTime().Sub(requests[0].GetInterval().GetStartTime().AsTime())
	assert.Equal(t, 5*time.Minute, window)
	dp := metrics.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics().At(0).Gauge().DataPoints().At(0)
	_, hasStat := dp.Attributes().Get("stat")
	assert.False(t, hasStat, "no stat attribute without aggregation")
	assert.InDelta(t, 0.7, dp.DoubleValue(), 1e-9)
}

func TestListOfTwoProjectsRequestsBoth(t *testing.T) {
	cfg := aggregationConfig([]string{"proj-a", "proj-b"}, []string{"ALIGN_MIN", "ALIGN_MAX"}, "m.googleapis.com/a")
	h := newHarness(t, cfg, gaugeDescriptor("m.googleapis.com/a"))

	metrics, err := h.mr.Scrape(context.Background())

	require.NoError(t, err)
	var names []string
	for _, req := range h.fake.recorded() {
		names = append(names, req.GetName())
	}
	sort.Strings(names)
	assert.Equal(t, []string{"projects/proj-a", "projects/proj-a", "projects/proj-b", "projects/proj-b"}, names)
	assert.Equal(t, 4, metrics.ResourceMetrics().Len(), "one resource per project and aligner")
}

func TestPercentileOfAMetricWithoutPercentilesGivesNoPointAndOneInfo(t *testing.T) {
	cfg := aggregationConfig([]string{"proj-a", "proj-b", "proj-c"}, []string{"ALIGN_PERCENTILE_50"}, "m.googleapis.com/a")
	h := newHarness(t, cfg, gaugeDescriptor("m.googleapis.com/a"))

	metrics, err := h.mr.Scrape(context.Background())

	require.NoError(t, err, "no error")
	assert.Zero(t, metrics.ResourceMetrics().Len(), "no point")
	assert.Empty(t, h.fake.recorded(), "the unsupported request is not sent")
	infos := h.logs.FilterMessageSnippet("percentile").FilterLevelExact(zapcore.InfoLevel).All()
	require.Len(t, infos, 1, "one INFO line for three projects")
	assert.Equal(t, "m.googleapis.com/a", infos[0].ContextMap()["metric"])
	assert.Empty(t, h.logs.FilterLevelExact(zapcore.WarnLevel).All())
	assert.Empty(t, h.logs.FilterLevelExact(zapcore.ErrorLevel).All())
}

func TestPercentileInfoComesAgainOnTheNextRun(t *testing.T) {
	cfg := aggregationConfig([]string{"proj-a"}, []string{"ALIGN_PERCENTILE_50"}, "m.googleapis.com/a")
	h := newHarness(t, cfg, gaugeDescriptor("m.googleapis.com/a"))

	_, err := h.mr.Scrape(context.Background())
	require.NoError(t, err)
	_, err = h.mr.Scrape(context.Background())
	require.NoError(t, err)

	assert.Len(t, h.logs.FilterMessageSnippet("percentile").All(), 2, "one INFO line per metric name per run")
}

func TestRejectedRequestIsLoggedAndTheOthersContinue(t *testing.T) {
	cfg := aggregationConfig([]string{"proj-a"}, []string{"ALIGN_MIN", "ALIGN_SUM", "ALIGN_MAX"}, "m.googleapis.com/a")
	h := newHarness(t, cfg, gaugeDescriptor("m.googleapis.com/a"))
	h.fake.failFor = func(req *monitoringpb.ListTimeSeriesRequest) error {
		if req.GetAggregation().GetPerSeriesAligner() == monitoringpb.Aggregation_ALIGN_SUM {
			return status.Error(codes.InvalidArgument, "aligner not supported for this metric kind")
		}
		return nil
	}

	metrics, err := h.mr.Scrape(context.Background())

	require.NoError(t, err)
	assert.Equal(t, map[string]float64{"min": 1, "max": 5}, scrapedGauges(t, metrics))
	warnings := h.logs.FilterLevelExact(zapcore.WarnLevel).All()
	require.Len(t, warnings, 1)
	assert.Equal(t, "ALIGN_SUM", warnings[0].ContextMap()["aligner"])
	assert.Equal(t, "m.googleapis.com/a", warnings[0].ContextMap()["metric"])
	assert.Contains(t, warnings[0].ContextMap()["error"], "aligner not supported")
}

func TestPercentileOfADistributionIsSent(t *testing.T) {
	cfg := aggregationConfig([]string{"proj-a"}, []string{"ALIGN_PERCENTILE_50"}, "m.googleapis.com/dist")
	h := newHarness(t, cfg, distributionDescriptor("m.googleapis.com/dist"))

	_, err := h.mr.Scrape(context.Background())

	require.NoError(t, err)
	requests := h.fake.recorded()
	require.Len(t, requests, 1)
	assert.Equal(t, monitoringpb.Aggregation_ALIGN_PERCENTILE_50, requests[0].GetAggregation().GetPerSeriesAligner())
}

func TestCountResultIsAnIntegerGaugeWithUnitOne(t *testing.T) {
	cfg := aggregationConfig([]string{"proj-a"}, []string{"ALIGN_COUNT"}, "m.googleapis.com/dist")
	desc := distributionDescriptor("m.googleapis.com/dist")
	h := newHarness(t, cfg, desc)

	metrics, err := h.mr.Scrape(context.Background())

	require.NoError(t, err)
	m := metrics.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics().At(0)
	assert.Equal(t, "1", m.Unit(), "a count has no unit of the metric")
	dp := m.Gauge().DataPoints().At(0)
	assert.Equal(t, pmetric.NumberDataPointValueTypeInt, dp.ValueType())
	assert.Equal(t, int64(3), dp.IntValue())
}

func TestDescriptorsAreReadForEveryProject(t *testing.T) {
	cfg := aggregationConfig([]string{"proj-a", "proj-b"}, []string{"ALIGN_MAX"}, "m.googleapis.com/a")
	h := newHarness(t, cfg, gaugeDescriptor("m.googleapis.com/a"))

	h.fake.mu.Lock()
	got := append([]string(nil), h.fake.descriptorProjects...)
	h.fake.mu.Unlock()
	sort.Strings(got)
	assert.Equal(t, []string{"projects/proj-a", "projects/proj-b"}, got)
	assert.Contains(t, h.mr.metricDescriptors["proj-a"], "m.googleapis.com/a")
	assert.Contains(t, h.mr.metricDescriptors["proj-b"], "m.googleapis.com/a")
}

func denyProject(project string) func(*fakeMonitoring) {
	return func(f *fakeMonitoring) {
		f.failDescriptors = func(req *monitoringpb.ListMetricDescriptorsRequest) error {
			if req.GetName() == "projects/"+project {
				return status.Error(codes.PermissionDenied, "the caller does not have permission")
			}
			return nil
		}
		f.failFor = func(req *monitoringpb.ListTimeSeriesRequest) error {
			if req.GetName() == "projects/"+project {
				return status.Error(codes.PermissionDenied, "the caller does not have permission")
			}
			return nil
		}
	}
}

func TestProjectThatCannotListDescriptorsIsSkippedWithAWarning(t *testing.T) {
	cfg := aggregationConfig([]string{"proj-a", "proj-b"}, []string{"ALIGN_MAX"}, "m.googleapis.com/a")
	h, err := newHarnessWith(t, cfg, denyProject("proj-b"), gaugeDescriptor("m.googleapis.com/a"))

	require.NoError(t, err, "one readable project is enough to start")
	warnings := h.logs.FilterLevelExact(zapcore.WarnLevel).All()
	require.Len(t, warnings, 1)
	assert.Equal(t, "proj-b", warnings[0].ContextMap()["project"])
	assert.Contains(t, warnings[0].ContextMap()["error"], "does not have permission")

	metrics, err := h.mr.Scrape(context.Background())

	require.NoError(t, err)
	assert.Equal(t, 1, metrics.ResourceMetrics().Len(), "the other project is scraped")
	requests := h.fake.recorded()
	require.NotEmpty(t, requests, "the scrape sent no request")
	for _, req := range requests {
		assert.Equal(t, "projects/proj-a", req.GetName(), "the skipped project gets no time series request")
	}
}

func TestStartFailsWhenNoProjectCanListDescriptors(t *testing.T) {
	cfg := aggregationConfig([]string{"proj-a", "proj-b"}, []string{"ALIGN_MAX"}, "m.googleapis.com/a")
	denyAll := func(f *fakeMonitoring) {
		f.failDescriptors = func(*monitoringpb.ListMetricDescriptorsRequest) error {
			return status.Error(codes.PermissionDenied, "denied")
		}
	}

	_, err := newHarnessWith(t, cfg, denyAll, gaugeDescriptor("m.googleapis.com/a"))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "proj-a")
	assert.Contains(t, err.Error(), "proj-b")
}

func TestScrapeWithoutAggregationSkipsAFailingProjectAndKeepsTheOther(t *testing.T) {
	cfg := baseConfig()
	cfg.Aggregation = nil
	cfg.ControllerConfig.CollectionInterval = 5 * time.Minute
	cfg.Projects = []string{"proj-a", "proj-b"}
	cfg.MetricsList = metricConfigs("m.googleapis.com/a")
	h := newHarness(t, cfg, gaugeDescriptor("m.googleapis.com/a"))
	h.fake.failFor = func(req *monitoringpb.ListTimeSeriesRequest) error {
		if req.GetName() == "projects/proj-b" {
			return status.Error(codes.PermissionDenied, "denied")
		}
		return nil
	}

	metrics, err := h.mr.Scrape(context.Background())

	require.NoError(t, err)
	assert.Equal(t, 1, metrics.ResourceMetrics().Len())
	warnings := h.logs.FilterLevelExact(zapcore.WarnLevel).All()
	require.Len(t, warnings, 1)
	assert.Equal(t, "proj-b", warnings[0].ContextMap()["project"])
}

func TestScrapeWithoutAggregationReturnsTheErrorWhenEveryProjectFails(t *testing.T) {
	cfg := baseConfig()
	cfg.Aggregation = nil
	cfg.ControllerConfig.CollectionInterval = 5 * time.Minute
	cfg.MetricsList = metricConfigs("m.googleapis.com/a")
	h := newHarness(t, cfg, gaugeDescriptor("m.googleapis.com/a"))
	h.fake.failFor = func(*monitoringpb.ListTimeSeriesRequest) error {
		return status.Error(codes.PermissionDenied, "denied")
	}

	_, err := h.mr.Scrape(context.Background())

	require.Error(t, err, "a single project that fails behaves as before")
	assert.Contains(t, err.Error(), "failed to retrieve time series data")
}
