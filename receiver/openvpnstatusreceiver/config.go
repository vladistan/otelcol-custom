package openvpnstatusreceiver

import (
	"errors"
	"time"

	"go.opentelemetry.io/collector/component"
)

type Config struct {
	Path             string        `mapstructure:"path"`
	PollInterval     time.Duration `mapstructure:"poll_interval"`
	JoinRoutingTable bool          `mapstructure:"join_routing_table"`
}

var _ component.Config = (*Config)(nil)

func (cfg *Config) Validate() error {
	if cfg.Path == "" {
		return errors.New("path must be set")
	}
	if cfg.PollInterval <= 0 {
		return errors.New("poll_interval must be positive")
	}
	return nil
}
