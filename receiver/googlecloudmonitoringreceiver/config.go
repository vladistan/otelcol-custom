// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0
// Modified by V-Lad: adds the aggregation block and a project list.

package googlecloudmonitoringreceiver // import "github.com/open-telemetry/opentelemetry-collector-contrib/receiver/googlecloudmonitoringreceiver"

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"go.opentelemetry.io/collector/scraper/scraperhelper"
)

const (
	defaultCollectionInterval = 300 * time.Second // Default value for collection interval
	minCollectionInterval     = 60 * time.Second  // Minimum value for collection interval
	defaultFetchDelay         = 60 * time.Second  // Default value for fetch delay
)

type Config struct {
	ControllerConfig scraperhelper.ControllerConfig `mapstructure:",squash"`

	ProjectID string `mapstructure:"project_id"`
	// Projects lists more projects for the same instance; it adds to project_id.
	Projects []string `mapstructure:"projects"`
	// Overrides the default monitoring.googleapis.com:443 endpoint.
	// Use this when targeting non-standard universe domains.
	Endpoint string `mapstructure:"endpoint"`
	// UniverseDomain is the universe domain for the Google Cloud Monitoring service.
	// Defaults to "googleapis.com". Set to support Sovereign Cloud regions.
	// See https://pkg.go.dev/google.golang.org/api/option#WithUniverseDomain
	UniverseDomain string         `mapstructure:"universe_domain"`
	MetricsList    []MetricConfig `mapstructure:"metrics_list"`
	// Aggregation asks Cloud Monitoring for per-series aligned results and lifts the 23 h window cap.
	Aggregation *AggregationConfig `mapstructure:"aggregation"`
}

type MetricConfig struct {
	MetricName string `mapstructure:"metric_name"`
	// Filter for listing metric descriptors. Only support `project` and `metric.type` as filter objects.
	// See https://cloud.google.com/monitoring/api/v3/filters#metric-descriptor-filter for more details.
	MetricDescriptorFilter string `mapstructure:"metric_descriptor_filter"`
}

func (config *Config) Validate() error {
	if config.ControllerConfig.CollectionInterval < minCollectionInterval {
		return fmt.Errorf("\"collection_interval\" must be not lower than the collection interval: %v, current value is %v", minCollectionInterval, config.ControllerConfig.CollectionInterval)
	}

	if len(config.MetricsList) == 0 {
		return errors.New("missing required field \"metrics_list\" or its value is empty")
	}

	for _, metric := range config.MetricsList {
		if err := metric.Validate(); err != nil {
			return err
		}
	}

	for _, project := range config.Projects {
		if strings.TrimSpace(project) == "" {
			return errors.New("\"projects\" must not contain an empty name")
		}
	}

	if len(config.projectList()) == 0 {
		return errors.New("one of \"project_id\" or \"projects\" is required")
	}

	if config.Aggregation == nil {
		if config.ControllerConfig.CollectionInterval > maxQueryWindow {
			return fmt.Errorf("\"collection_interval\" %v is above %v; a longer window needs \"aggregation\"", config.ControllerConfig.CollectionInterval, maxQueryWindow)
		}
		return nil
	}

	if err := config.Aggregation.Validate(); err != nil {
		return err
	}
	if config.Aggregation.AlignmentPeriod > config.ControllerConfig.CollectionInterval {
		return fmt.Errorf("\"aggregation.alignment_period\" %v is longer than \"collection_interval\" %v, so the window holds only part of one bucket", config.Aggregation.AlignmentPeriod, config.ControllerConfig.CollectionInterval)
	}
	return nil
}

// projectList returns project_id followed by the entries of projects, without repeats or empty names.
func (config *Config) projectList() []string {
	var out []string
	seen := map[string]bool{}
	for _, project := range append([]string{config.ProjectID}, config.Projects...) {
		if project == "" || seen[project] {
			continue
		}
		seen[project] = true
		out = append(out, project)
	}
	return out
}

func (metric MetricConfig) Validate() error {
	if metric.MetricName != "" && metric.MetricDescriptorFilter != "" {
		return errors.New("fields \"metric_name\" and \"metric_descriptor_filter\" cannot both have value")
	}

	if metric.MetricName == "" && metric.MetricDescriptorFilter == "" {
		return errors.New("fields \"metric_name\" and \"metric_descriptor_filter\" cannot both be empty")
	}

	return nil
}
