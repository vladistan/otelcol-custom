package psireceiver

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/bmatcuk/doublestar/v4"
	"go.opentelemetry.io/collector/component"
)

const (
	levelPod       = "pod"
	levelContainer = "container"
	levelAll       = "all"

	valueAvg10  = "avg10"
	valueAvg60  = "avg60"
	valueAvg300 = "avg300"
	valueTotal  = "total"
)

// Config defines configuration for the PSI receiver.
type Config struct {
	// CollectionInterval is the interval at which to collect PSI metrics.
	CollectionInterval time.Duration `mapstructure:"collection_interval"`

	// ProcPath is the path to the proc filesystem. Defaults to "/proc".
	// Can be overridden for testing or containerized environments.
	ProcPath string `mapstructure:"proc_path"`

	// Cgroup turns on per-cgroup pressure from the cgroup v2 tree. It is off by default.
	Cgroup CgroupConfig `mapstructure:"cgroup"`
}

// CgroupConfig selects the cgroups whose pressure files are read.
type CgroupConfig struct {
	// Enabled turns the block on. Include must then name the cgroups to read.
	Enabled bool `mapstructure:"enabled"`
	// Path is the cgroup v2 root, for example a read-only host mount.
	Path string `mapstructure:"path"`
	// MaxDepth is how many levels below Path are read; the root itself is not read.
	MaxDepth int `mapstructure:"max_depth"`
	// Level is pod, container or all.
	Level string `mapstructure:"level"`
	// Include and Exclude are doublestar globs matched against the path relative to Path.
	Include []string `mapstructure:"include"`
	Exclude []string `mapstructure:"exclude"`
	// Values lists which of avg10, avg60, avg300 and total are emitted.
	Values []string `mapstructure:"values"`
	// MaxCgroups caps the cgroups read in one scrape.
	MaxCgroups int `mapstructure:"max_cgroups"`
}

func defaultCgroupConfig() CgroupConfig {
	return CgroupConfig{
		Path:       "/sys/fs/cgroup",
		MaxDepth:   4,
		Level:      levelPod,
		Values:     []string{valueAvg10, valueTotal},
		MaxCgroups: 500,
	}
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
	if cfg.Cgroup.Enabled {
		return cfg.Cgroup.validate()
	}
	return nil
}

func (c *CgroupConfig) validate() error {
	if c.Path == "" {
		return errors.New("cgroup.path must not be empty")
	}
	if len(c.Include) == 0 {
		return errors.New("cgroup.include must list at least one glob when cgroup.enabled is true")
	}
	for _, pattern := range c.Include {
		if !doublestar.ValidatePattern(pattern) {
			return fmt.Errorf("cgroup.include has an invalid glob %q", pattern)
		}
	}
	for _, pattern := range c.Exclude {
		if !doublestar.ValidatePattern(pattern) {
			return fmt.Errorf("cgroup.exclude has an invalid glob %q", pattern)
		}
	}
	if !slices.Contains([]string{levelPod, levelContainer, levelAll}, c.Level) {
		return fmt.Errorf("cgroup.level must be pod, container or all, not %q", c.Level)
	}
	if len(c.Values) == 0 {
		return errors.New("cgroup.values must list at least one of avg10, avg60, avg300 and total")
	}
	for _, value := range c.Values {
		if !slices.Contains([]string{valueAvg10, valueAvg60, valueAvg300, valueTotal}, value) {
			return fmt.Errorf("cgroup.values has an unknown value %q", value)
		}
	}
	if c.MaxDepth < 1 {
		return errors.New("cgroup.max_depth must be at least 1")
	}
	if c.MaxCgroups < 1 {
		return errors.New("cgroup.max_cgroups must be at least 1")
	}
	return nil
}
