// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

//go:build !freebsd

package zfskstatreceiver

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap/zapcore"
)

func TestSysctlSourceOffFreeBSDLogsTheInfoLineAndSendsNothing(t *testing.T) {
	cfg := sysctlConfig()
	s, logs := newTestScraper(cfg)

	md := scrapeOnce(t, s)
	scrapeOnce(t, s)

	assert.Equal(t, 0, md.ResourceMetrics().Len())
	assert.Equal(t, 1, logs.FilterLevelExact(zapcore.InfoLevel).Len())
	assert.Empty(t, warnings(logs))
	assert.False(t, s.start.After(s.started))
}
