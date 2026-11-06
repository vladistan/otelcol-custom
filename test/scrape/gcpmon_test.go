// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package scrape

import (
	"slices"
	"testing"
)

var cloudRunMetrics = []string{
	"run.googleapis.com/request_count",
	"run.googleapis.com/request_latencies",
	"run.googleapis.com/container/cpu/utilizations",
	"run.googleapis.com/container/memory/utilizations",
	"run.googleapis.com/container/instance_count",
}

func TestCloudRunMetricsReceivers(t *testing.T) {
	cfg := loadReference(t)
	want := map[string]string{
		"googlecloudmonitoring/cloudrun-a": "${env:GCP_MON_PROJECT_A}",
		"googlecloudmonitoring/cloudrun-b": "${env:GCP_MON_PROJECT_B}",
	}
	for name, project := range want {
		block := section(t, cfg, "receivers", name)
		if block["project_id"] != project {
			t.Errorf("%s project_id = %v, want %s", name, block["project_id"], project)
		}
		if block["collection_interval"] != "5m" {
			t.Errorf("%s collection_interval = %v, want 5m", name, block["collection_interval"])
		}
		var got []string
		for _, item := range block["metrics_list"].([]any) {
			got = append(got, item.(map[string]any)["metric_name"].(string))
		}
		if !slices.Equal(got, cloudRunMetrics) {
			t.Errorf("%s metrics = %v, want %v", name, got, cloudRunMetrics)
		}
	}
}

func TestCloudRunMetricsPipeline(t *testing.T) {
	pipelines := section(t, loadReference(t), "service", "pipelines")
	pipe, ok := pipelines["metrics/gcp"].(map[string]any)
	if !ok {
		t.Fatal("service.pipelines.metrics/gcp is missing")
	}
	receivers := toStrings(pipe["receivers"])
	for _, name := range []string{"googlecloudmonitoring/cloudrun-a", "googlecloudmonitoring/cloudrun-b"} {
		if !slices.Contains(receivers, name) {
			t.Errorf("metrics/gcp receivers = %v, want %s", receivers, name)
		}
	}
	if got := toStrings(pipe["processors"]); !slices.Equal(got, []string{"memory_limiter", "batch"}) {
		t.Errorf("metrics/gcp processors = %v, want [memory_limiter batch]", got)
	}
	if got := toStrings(pipe["exporters"]); !slices.Equal(got, []string{"otlp_http"}) {
		t.Errorf("metrics/gcp exporters = %v, want [otlp_http]", got)
	}
}

func toStrings(v any) []string {
	var out []string
	for _, item := range v.([]any) {
		out = append(out, item.(string))
	}
	return out
}
