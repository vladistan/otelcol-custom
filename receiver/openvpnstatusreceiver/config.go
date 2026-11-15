package openvpnstatusreceiver

import (
	"errors"
	"time"

	"go.opentelemetry.io/collector/component"
)

// The signals the receiver emits.
const (
	outputMetrics = "metrics"
	outputLogs    = "logs"
	outputBoth    = "both"
)

type Config struct {
	Path             string        `mapstructure:"path"`
	PollInterval     time.Duration `mapstructure:"poll_interval"`
	JoinRoutingTable bool          `mapstructure:"join_routing_table"`
	// Output is unset (default), metrics, logs or both. Unset, the receiver
	// emits the signal of each pipeline it is in. A named output restricts it,
	// and a pipeline of a signal the output leaves out is refused at start.
	Output string `mapstructure:"output"`
}

var _ component.Config = (*Config)(nil)

func (cfg *Config) Validate() error {
	if cfg.Path == "" {
		return errors.New("path must be set")
	}
	if cfg.PollInterval <= 0 {
		return errors.New("poll_interval must be positive")
	}
	switch cfg.Output {
	case "", outputMetrics, outputLogs, outputBoth:
		return nil
	}
	return errors.New("output must be unset or one of metrics, logs or both")
}
