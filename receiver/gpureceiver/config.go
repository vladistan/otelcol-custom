// Modified: per-metric toggles keyed by emitted metric name; unknown keys rejected.
// Original: https://github.com/pmady/otel-gpu-receiver@ac756935ca040923d98af5a0d0547dca02a3b23b/config.go

package gpureceiver

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"go.opentelemetry.io/collector/scraper/scraperhelper"

	"github.com/vladistan/otelcol-custom/receiver/gpureceiver/internal/port"
	"github.com/vladistan/otelcol-custom/receiver/gpureceiver/internal/semconv"
)

// Config holds the scrape controller settings and the per-metric toggles.
type Config struct {
	scraperhelper.ControllerConfig `mapstructure:",squash"`

	// Metrics maps an emitted metric name to its toggle.
	Metrics map[string]MetricConfig `mapstructure:"metrics"`
}

// MetricConfig controls whether an individual metric is enabled.
type MetricConfig struct {
	Enabled bool `mapstructure:"enabled"`
}

func defaultMetricsConfig() map[string]MetricConfig {
	metrics := make(map[string]MetricConfig)
	for _, key := range semconv.ToggleKeys() {
		metrics[key] = MetricConfig{Enabled: true}
	}
	return metrics
}

func createDefaultConfig() *Config {
	cfg := scraperhelper.NewDefaultControllerConfig()
	cfg.CollectionInterval = 10 * time.Second

	return &Config{
		ControllerConfig: cfg,
		Metrics:          defaultMetricsConfig(),
	}
}

// Validate rejects a non-positive collection_interval and any metrics key
// that is not an emitted metric name.
func (cfg *Config) Validate() error {
	if cfg.CollectionInterval <= 0 {
		return errors.New("collection_interval must be a positive duration")
	}
	known := semconv.ToggleKeys()
	var unknown []string
	for key := range cfg.Metrics {
		if !slices.Contains(known, key) {
			unknown = append(unknown, key)
		}
	}
	if len(unknown) > 0 {
		slices.Sort(unknown)
		return fmt.Errorf("unknown metric keys under metrics: %s", strings.Join(unknown, ", "))
	}
	return nil
}

// Toggles copies the configured metrics into the Collector's toggle set.
func (cfg *Config) Toggles() port.Toggles {
	toggles := make(port.Toggles, len(cfg.Metrics))
	for key, metric := range cfg.Metrics {
		toggles[key] = metric.Enabled
	}
	return toggles
}
