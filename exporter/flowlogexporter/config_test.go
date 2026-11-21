// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package flowlogexporter

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component/componenttest"
)

func TestDefaultConfig_NeedsAnEndpointAndHasABoundedQueueAndALimitedRetry(t *testing.T) {
	cfg := createDefaultConfig().(*Config)

	assert.Error(t, cfg.Validate(), "no endpoint")
	cfg.Endpoint = "clickhouse.example.invalid:9000"
	require.NoError(t, cfg.Validate())
	require.NotNil(t, cfg.QueueSettings.Get())
	assert.Positive(t, cfg.QueueSettings.Get().QueueSize)
	assert.True(t, cfg.BackOffConfig.Enabled)
	assert.Positive(t, cfg.BackOffConfig.MaxElapsedTime)
	assert.Equal(t, "flowlogs", cfg.Database)
	assert.Equal(t, "flow_log", cfg.Table)
	assert.Equal(t, "flowlogexporter", cfg.Loader)
}

func TestValidate_RejectsWhatCannotWork(t *testing.T) {
	cases := map[string]func(*Config){
		"empty endpoint":      func(c *Config) { c.Endpoint = "" },
		"database with a dot": func(c *Config) { c.Database = "a.b" },
		"table with a quote":  func(c *Config) { c.Table = "t`; DROP" },
		"invalid address":     func(c *Config) { c.Routers = []Router{{Address: "not-an-ip"}} },
		"duplicate address":   func(c *Config) { c.Routers = []Router{{Address: "192.0.2.1"}, {Address: "192.0.2.1"}} },
		"duplicate in v4-mapped form": func(c *Config) {
			c.Routers = []Router{{Address: "192.0.2.1"}, {Address: "::ffff:192.0.2.1"}}
		},
		"negative sampling rate": func(c *Config) { c.Routers = []Router{{Address: "192.0.2.1", SamplingRate: -1}} },
		"zero queue size":        func(c *Config) { c.QueueSettings.Get().QueueSize = 0 },
	}
	for name, mutate := range cases {
		cfg := testConfig()
		mutate(cfg)

		assert.Error(t, cfg.Validate(), name)
	}
}

func TestValidate_AcceptsTwoDifferentRouters(t *testing.T) {
	cfg := testConfig()
	cfg.Routers = []Router{{Address: "192.0.2.1"}, {Address: "2001:db8::1"}}

	assert.NoError(t, cfg.Validate())
}

func TestFactory_TypeConfigAndLogsExporter(t *testing.T) {
	f := NewFactory()

	assert.Equal(t, "flowlog", f.Type().String())
	require.NoError(t, componenttest.CheckConfigStruct(f.CreateDefaultConfig()))
	cfg := testConfig()
	set, _, _ := rigSettings()
	exp, err := f.CreateLogs(context.Background(), set, cfg)
	require.NoError(t, err)
	assert.NotNil(t, exp)
}
