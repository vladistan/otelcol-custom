// Modified: toggles are a name-keyed map; unknown-key and Toggles tests added.
// Original: https://github.com/pmady/otel-gpu-receiver@ac756935ca040923d98af5a0d0547dca02a3b23b/config_test.go

package gpureceiver

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/collector/scraper/scraperhelper"

	"github.com/vladistan/otelcol-custom/receiver/gpureceiver/internal/semconv"
)

func TestDefaultConfig(t *testing.T) {
	cfg := createDefaultConfig()

	assert.Equal(t, 10*time.Second, cfg.CollectionInterval)
	assert.Len(t, cfg.Metrics, len(semconv.ToggleKeys()))
	for _, key := range semconv.ToggleKeys() {
		assert.True(t, cfg.Metrics[key].Enabled, "metric %s should default to enabled", key)
	}
}

func TestToggles(t *testing.T) {
	cfg := createDefaultConfig()
	cfg.Metrics[semconv.MetricClock] = MetricConfig{Enabled: false}

	toggles := cfg.Toggles()

	assert.Len(t, toggles, len(semconv.ToggleKeys()))
	assert.False(t, toggles[semconv.MetricClock])
	assert.True(t, toggles[semconv.MetricIO])
}

func TestConfig_Validate(t *testing.T) {
	withInterval := func(d time.Duration) *Config {
		c := scraperhelper.NewDefaultControllerConfig()
		c.CollectionInterval = d
		return &Config{ControllerConfig: c, Metrics: defaultMetricsConfig()}
	}
	withUnknown := func(keys ...string) *Config {
		cfg := createDefaultConfig()
		for _, key := range keys {
			cfg.Metrics[key] = MetricConfig{Enabled: true}
		}
		return cfg
	}

	valid := map[string]*Config{
		"default config":  createDefaultConfig(),
		"custom interval": withInterval(30 * time.Second),
	}
	for name, cfg := range valid {
		t.Run("valid "+name, func(t *testing.T) {
			assert.NoError(t, cfg.Validate())
		})
	}

	invalid := []struct {
		name    string
		cfg     *Config
		wantErr string
	}{
		{"zero interval", withInterval(0), "collection_interval"},
		{"negative interval", withInterval(-5 * time.Second), "collection_interval"},
		{"unknown metric key", withUnknown("gpu.ecc.errors"), "gpu.ecc.errors"},
		{"unknown keys listed sorted", withUnknown("zzz", "aaa"), "aaa, zzz"},
	}
	for _, tt := range invalid {
		t.Run("invalid "+tt.name, func(t *testing.T) {
			assert.ErrorContains(t, tt.cfg.Validate(), tt.wantErr)
		})
	}
}

func TestValidateLibraryPath(t *testing.T) {
	tests := []struct {
		name    string
		goos    string
		path    string
		wantErr string
	}{
		{"unset on windows", "windows", "", ""},
		{"unset on linux", "linux", "", ""},
		{"absolute drive path on windows", "windows", `C:\Program Files\NVIDIA Corporation\NVSMI\nvml.dll`, ""},
		{"relative path on windows", "windows", `nvml.dll`, "absolute"},
		{"set on linux", "linux", `C:\nvml.dll`, "only supported on windows"},
		{"set on darwin", "darwin", `C:\nvml.dll`, "only supported on windows"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateLibraryPath(tt.goos, tt.path)

			if tt.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			assert.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestDefaultConfigLeavesLibraryPathUnset(t *testing.T) {
	assert.Empty(t, createDefaultConfig().LibraryPath)
}
