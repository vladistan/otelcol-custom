// Modified: per-metric toggles keyed by emitted metric name; unknown keys rejected.
// Original: https://github.com/pmady/otel-gpu-receiver@ac756935ca040923d98af5a0d0547dca02a3b23b/config.go

package gpureceiver

import (
	"errors"
	"fmt"
	"runtime"
	"slices"
	"strings"
	"time"

	"go.opentelemetry.io/collector/scraper/scraperhelper"

	"github.com/vladistan/otelcol-custom/receiver/gpureceiver/internal/nvmladapter"
	"github.com/vladistan/otelcol-custom/receiver/gpureceiver/internal/port"
	"github.com/vladistan/otelcol-custom/receiver/gpureceiver/internal/semconv"
)

// Config holds the scrape controller settings and the per-metric toggles.
type Config struct {
	scraperhelper.ControllerConfig `mapstructure:",squash"`

	// Metrics maps an emitted metric name to its toggle.
	Metrics map[string]MetricConfig `mapstructure:"metrics"`

	// LibraryPath is the absolute path of nvml.dll. Windows only. Empty loads
	// nvml.dll from System32.
	LibraryPath string `mapstructure:"library_path"`
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

// validateLibraryPath accepts an empty path anywhere and an absolute path on
// windows. A relative path would let LoadLibrary search the working directory.
func validateLibraryPath(goos, path string) error {
	switch {
	case path == "":
		return nil
	case goos != "windows":
		return errors.New("library_path is only supported on windows")
	case !nvmladapter.IsWindowsAbsPath(path):
		return errors.New("library_path must be an absolute path")
	}
	return nil
}

// Validate rejects a non-positive collection_interval, a library_path that is
// not usable on this OS, and any metrics key that is not an emitted metric name.
func (cfg *Config) Validate() error {
	if cfg.CollectionInterval <= 0 {
		return errors.New("collection_interval must be a positive duration")
	}
	if err := validateLibraryPath(runtime.GOOS, cfg.LibraryPath); err != nil {
		return err
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
