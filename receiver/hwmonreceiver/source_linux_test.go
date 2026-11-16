// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package hwmonreceiver

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/receiver/receivertest"
)

func TestLinuxFactoryReadsTheConfiguredPath(t *testing.T) {
	f := newTree(t)
	f.chip("hwmon0", "coretemp", map[string]string{"temp1_input": "45500", "temp1_label": "Package id 0"})
	fac := NewFactory()
	cfg := fac.CreateDefaultConfig().(*Config)
	cfg.HwmonPath = f.hwmonPath()

	r, err := fac.CreateMetrics(context.Background(), receivertest.NewNopSettings(fac.Type()), cfg, consumertest.NewNop())

	require.NoError(t, err)
	assert.NotNil(t, r)
}
