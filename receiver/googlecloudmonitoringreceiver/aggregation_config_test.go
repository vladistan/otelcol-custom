// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package googlecloudmonitoringreceiver

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/confmap"
	"go.opentelemetry.io/collector/scraper/scraperhelper"
)

func baseConfig() *Config {
	cfg := scraperhelper.NewDefaultControllerConfig()
	cfg.CollectionInterval = 24 * time.Hour
	return &Config{
		ControllerConfig: cfg,
		Projects:         []string{"proj-a"},
		MetricsList:      []MetricConfig{{MetricName: "run.googleapis.com/container/cpu/utilizations"}},
		Aggregation: &AggregationConfig{
			AlignmentPeriod:   24 * time.Hour,
			PerSeriesAligners: []string{"ALIGN_MIN", "ALIGN_MAX"},
		},
	}
}

func TestAggregationConfigIsAccepted(t *testing.T) {
	assert.NoError(t, baseConfig().Validate())
}

func TestEveryListedAlignerIsAccepted(t *testing.T) {
	cfg := baseConfig()
	cfg.Aggregation.PerSeriesAligners = []string{
		"ALIGN_MIN", "ALIGN_MAX", "ALIGN_MEAN", "ALIGN_SUM", "ALIGN_COUNT", "ALIGN_PERCENTILE_50",
	}

	assert.NoError(t, cfg.Validate())
}

func TestAlignerOutsideTheListIsRejected(t *testing.T) {
	cfg := baseConfig()
	cfg.Aggregation.PerSeriesAligners = []string{"ALIGN_MIN", "ALIGN_RATE"}

	err := cfg.Validate()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "ALIGN_RATE")
}

func TestAggregationNeedsAligners(t *testing.T) {
	cfg := baseConfig()
	cfg.Aggregation.PerSeriesAligners = nil

	assert.ErrorContains(t, cfg.Validate(), "per_series_aligners")
}

func TestAggregationRejectsADuplicateAligner(t *testing.T) {
	cfg := baseConfig()
	cfg.Aggregation.PerSeriesAligners = []string{"ALIGN_MIN", "ALIGN_MIN"}

	assert.ErrorContains(t, cfg.Validate(), "ALIGN_MIN")
}

func TestAlignmentPeriodBelowOneMinuteIsRejected(t *testing.T) {
	for _, period := range []time.Duration{0, -time.Hour, 30 * time.Second} {
		cfg := baseConfig()
		cfg.Aggregation.AlignmentPeriod = period

		assert.ErrorContains(t, cfg.Validate(), "alignment_period", period.String())
	}
}

func TestWindowOf24HoursNeedsAggregation(t *testing.T) {
	withAggregation := baseConfig()
	assert.NoError(t, withAggregation.Validate(), "24 h with aggregation")

	without := baseConfig()
	without.Aggregation = nil
	assert.ErrorContains(t, without.Validate(), "collection_interval", "24 h without aggregation")

	atTheCap := baseConfig()
	atTheCap.Aggregation = nil
	atTheCap.ControllerConfig.CollectionInterval = 23 * time.Hour
	assert.NoError(t, atTheCap.Validate(), "23 h without aggregation")
}

func TestProjectsAndProjectIDAreMerged(t *testing.T) {
	cfg := baseConfig()
	cfg.ProjectID = "proj-b"
	cfg.Projects = []string{"proj-a", "proj-b", "proj-c"}

	assert.NoError(t, cfg.Validate())
	assert.Equal(t, []string{"proj-b", "proj-a", "proj-c"}, cfg.projectList())
}

func TestProjectIDAloneStillWorks(t *testing.T) {
	cfg := baseConfig()
	cfg.ProjectID = "only"
	cfg.Projects = nil

	assert.Equal(t, []string{"only"}, cfg.projectList())
}

func TestEmptyProjectNameInTheListIsRejected(t *testing.T) {
	cfg := baseConfig()
	cfg.Projects = []string{"proj-a", ""}

	assert.ErrorContains(t, cfg.Validate(), "projects")
}

func TestConfigLoadsFromTheConfigurationKeys(t *testing.T) {
	conf := confmap.NewFromStringMap(map[string]any{
		"collection_interval": "24h",
		"projects":            []any{"proj-a", "proj-b"},
		"metrics_list":        []any{map[string]any{"metric_name": "run.googleapis.com/container/cpu/utilizations"}},
		"aggregation": map[string]any{
			"alignment_period":    "24h",
			"per_series_aligners": []any{"ALIGN_MIN", "ALIGN_PERCENTILE_50"},
		},
	})
	cfg := createDefaultConfig().(*Config)

	require.NoError(t, conf.Unmarshal(cfg))

	assert.Equal(t, []string{"proj-a", "proj-b"}, cfg.Projects)
	require.NotNil(t, cfg.Aggregation)
	assert.Equal(t, 24*time.Hour, cfg.Aggregation.AlignmentPeriod)
	assert.Equal(t, []string{"ALIGN_MIN", "ALIGN_PERCENTILE_50"}, cfg.Aggregation.PerSeriesAligners)
	assert.NoError(t, cfg.Validate())
}

func TestStatNames(t *testing.T) {
	want := map[string]string{
		"ALIGN_MIN":           "min",
		"ALIGN_MAX":           "max",
		"ALIGN_MEAN":          "mean",
		"ALIGN_SUM":           "sum",
		"ALIGN_COUNT":         "count",
		"ALIGN_PERCENTILE_50": "p50",
	}

	for aligner, stat := range want {
		got, ok := statName(aligner)
		assert.True(t, ok, aligner)
		assert.Equal(t, stat, got, aligner)
	}
	_, ok := statName("ALIGN_RATE")
	assert.False(t, ok)
}

func TestNoProjectIsRejected(t *testing.T) {
	cfg := baseConfig()
	cfg.ProjectID = ""
	cfg.Projects = nil

	err := cfg.Validate()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "project_id")
	assert.Contains(t, err.Error(), "projects")
}

func TestProjectIDAloneOrProjectsAloneIsEnough(t *testing.T) {
	onlyID := baseConfig()
	onlyID.Projects = nil
	onlyID.ProjectID = "p"
	assert.NoError(t, onlyID.Validate())

	onlyList := baseConfig()
	assert.NoError(t, onlyList.Validate())
}

func TestAlignmentPeriodLongerThanTheWindowIsRejected(t *testing.T) {
	cfg := baseConfig()
	cfg.ControllerConfig.CollectionInterval = time.Hour
	cfg.Aggregation.AlignmentPeriod = 24 * time.Hour

	err := cfg.Validate()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "alignment_period")
	assert.Contains(t, err.Error(), "collection_interval")
}

func TestAlignmentPeriodEqualToOrShorterThanTheWindowIsAccepted(t *testing.T) {
	equal := baseConfig()
	assert.NoError(t, equal.Validate())

	shorter := baseConfig()
	shorter.Aggregation.AlignmentPeriod = time.Hour
	assert.NoError(t, shorter.Validate())
}
