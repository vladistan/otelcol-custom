// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package cloudwatchmetricsreceiver

import (
	"context"
	"maps"
	"slices"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
)

// seriesRef is one CloudWatch series: a metric with its own dimension set.
type seriesRef struct {
	namespace, name string
	dims            []types.Dimension
}

// discovered holds the series kept under the limit and the total found.
type discovered struct {
	series []seriesRef
	total  int
}

func (d discovered) truncated() bool { return d.total > len(d.series) }

// discoverSeries runs ListMetrics once per listed name, or once per namespace when
// its list is empty. Paging goes on past limit so the total is known, but only the
// first limit series are kept.
func discoverSeries(ctx context.Context, api metricsAPI, namespaces map[string][]string, limit int) (discovered, error) {
	var out discovered
	for _, ns := range slices.Sorted(maps.Keys(namespaces)) {
		names := namespaces[ns]
		if len(names) == 0 {
			names = []string{""}
		}
		for _, name := range names {
			if err := listInto(ctx, api, ns, name, limit, &out); err != nil {
				return discovered{}, err
			}
		}
	}
	return out, nil
}

func listInto(ctx context.Context, api metricsAPI, namespace, name string, limit int, out *discovered) error {
	in := &cloudwatch.ListMetricsInput{Namespace: aws.String(namespace)}
	if name != "" {
		in.MetricName = aws.String(name)
	}
	for {
		resp, err := api.ListMetrics(ctx, in)
		if err != nil {
			return err
		}
		for _, m := range resp.Metrics {
			out.total++
			if len(out.series) < limit {
				out.series = append(out.series, seriesRef{
					namespace: aws.ToString(m.Namespace), name: aws.ToString(m.MetricName), dims: slices.Clone(m.Dimensions),
				})
			}
		}
		if resp.NextToken == nil {
			return nil
		}
		in.NextToken = resp.NextToken
	}
}
