package openvpnstatusreceiver

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateRejectsAMissingPath(t *testing.T) {
	cfg := &Config{PollInterval: time.Second, Output: outputMetrics}

	assert.ErrorContains(t, cfg.Validate(), "path must be set")
}

func TestValidateRejectsAPollIntervalOfZeroOrLess(t *testing.T) {
	for _, interval := range []time.Duration{0, -time.Second} {
		cfg := &Config{Path: "/var/log/openvpn/status.log", PollInterval: interval, Output: outputMetrics}

		assert.ErrorContains(t, cfg.Validate(), "poll_interval must be positive", interval.String())
	}
}

func TestValidateAcceptsAPathAndAPositiveInterval(t *testing.T) {
	cfg := &Config{Path: "/var/log/openvpn/status.log", PollInterval: time.Millisecond, Output: outputMetrics}

	require.NoError(t, cfg.Validate())
}

func TestDefaultConfigLeavesOutputUnsetSoThePipelinesDecide(t *testing.T) {
	cfg := createDefaultConfig().(*Config)

	assert.Empty(t, cfg.Output)
}

func TestValidateAcceptsAnUnsetOutputAndEachNamedOne(t *testing.T) {
	for _, out := range []string{"", "metrics", "logs", "both"} {
		cfg := &Config{Path: "/p", PollInterval: time.Second, Output: out}

		assert.NoError(t, cfg.Validate(), out)
	}
}

func TestValidateRejectsAnUnknownOutput(t *testing.T) {
	for _, out := range []string{"log", "traces", "Metrics"} {
		cfg := &Config{Path: "/p", PollInterval: time.Second, Output: out}

		assert.ErrorContains(t, cfg.Validate(), "output must be unset or one of metrics, logs or both", out)
	}
}

func TestDefaultConfigPollsEvery20SecondsWithoutAJoin(t *testing.T) {
	cfg := createDefaultConfig().(*Config)

	assert.Equal(t, 20*time.Second, cfg.PollInterval)
	assert.False(t, cfg.JoinRoutingTable)
	assert.Empty(t, cfg.Path)
	assert.Error(t, cfg.Validate(), "path has no default")
}
