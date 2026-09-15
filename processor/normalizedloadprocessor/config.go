package normalizedloadprocessor

import (
	"go.opentelemetry.io/collector/component"
)

// Config defines configuration for the normalized load processor.
type Config struct {
	// CPUCount overrides auto-detected CPU count. Set to 0 for auto-detection.
	CPUCount int `mapstructure:"cpu_count"`
}

var _ component.Config = (*Config)(nil)

// Validate checks if the configuration is valid.
func (cfg *Config) Validate() error {
	// CPUCount of 0 means auto-detect, negative values are invalid but
	// we'll treat them as auto-detect as well
	return nil
}
