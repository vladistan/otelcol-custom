// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package googlecloudmonitoringreceiver

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/api/metric"
)

func newHarnessWithProjectDescriptors(t *testing.T, cfg *Config, perProject map[string]map[string]*metric.MetricDescriptor) *harness {
	t.Helper()
	h, err := newHarnessWith(t, cfg, func(f *fakeMonitoring) { f.projectDescriptors = perProject })
	require.NoError(t, err)
	return h
}

func TestTwoProjectsWithDifferentDescriptorsOfOneTypeKeepBoth(t *testing.T) {
	cfg := aggregationConfig([]string{"proj-a", "proj-b"}, []string{"ALIGN_MAX"}, "m.googleapis.com/a")
	h := newHarnessWithProjectDescriptors(t, cfg, map[string]map[string]*metric.MetricDescriptor{
		"proj-a": {"m.googleapis.com/a": gaugeDescriptor("m.googleapis.com/a")},
		"proj-b": {"m.googleapis.com/a": distributionDescriptor("m.googleapis.com/a")},
	})

	assert.Equal(t, "1", h.mr.metricDescriptors["proj-a"]["m.googleapis.com/a"].GetUnit())
	assert.Equal(t, "ms", h.mr.metricDescriptors["proj-b"]["m.googleapis.com/a"].GetUnit())
}

func TestATypeFoundInOneProjectIsNotRequestedFromTheOther(t *testing.T) {
	cfg := aggregationConfig([]string{"proj-a", "proj-b"}, []string{"ALIGN_MAX"}, "m.googleapis.com/a")
	h := newHarnessWithProjectDescriptors(t, cfg, map[string]map[string]*metric.MetricDescriptor{
		"proj-a": {"m.googleapis.com/a": gaugeDescriptor("m.googleapis.com/a")},
		"proj-b": {},
	})

	_, err := h.mr.Scrape(context.Background())

	require.NoError(t, err)
	requests := h.fake.recorded()
	require.Len(t, requests, 1)
	assert.Equal(t, "projects/proj-a", requests[0].GetName())
}

func TestAProjectWithNoDescriptorsStillStartsAndSendsNothing(t *testing.T) {
	cfg := aggregationConfig([]string{"proj-a"}, []string{"ALIGN_MAX"}, "m.googleapis.com/a")
	h := newHarnessWithProjectDescriptors(t, cfg, map[string]map[string]*metric.MetricDescriptor{"proj-a": {}})

	_, err := h.mr.Scrape(context.Background())

	require.NoError(t, err)
	assert.Empty(t, h.fake.recorded())
}
