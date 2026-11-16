// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

//go:build !linux && !freebsd

package hwmonreceiver

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/receiver/receivertest"
)

func TestOffLinuxAndFreeBSDTheReceiverCannotBeCreated(t *testing.T) {
	f := NewFactory()

	_, err := f.CreateMetrics(context.Background(), receivertest.NewNopSettings(f.Type()), f.CreateDefaultConfig(), consumertest.NewNop())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "Linux or FreeBSD")
}
