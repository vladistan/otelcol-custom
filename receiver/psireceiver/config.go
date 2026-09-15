package psireceiver

import (
	"errors"
	"time"

	"go.opentelemetry.io/collector/component"
)

// Config defines configuration for the PSI receiver.
type Config struct {
	// CollectionInterval is the interval at which to collect PSI metrics.
	CollectionInterval time.Duration `mapstructure:"collection_interval"`

	// ProcPath is the path to the proc filesystem. Defaults to "/proc".
	// Can be overridden for testing or containerized environments.
	ProcPath string `mapstructure:"proc_path"`
}

var _ component.Config = (*Config)(nil)

// Validate checks if the configuration is valid.
func (cfg *Config) Validate() error {
	if cfg.CollectionInterval <= 0 {
		return errors.New("collection_interval must be positive")
	}
	if cfg.CollectionInterval < time.Second {
		return errors.New("collection_interval must be at least 1 second")
	}
	return nil
}
