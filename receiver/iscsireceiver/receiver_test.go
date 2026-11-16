// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package iscsireceiver

import (
	"context"
	"errors"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component/componenttest"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/receiver/receivertest"
)

type failingSink struct{ calls atomic.Int32 }

func (*failingSink) Capabilities() consumer.Capabilities { return consumer.Capabilities{} }

func (f *failingSink) ConsumeMetrics(context.Context, pmetric.Metrics) error {
	f.calls.Add(1)
	return errors.New("downstream down")
}

func testConfig() *Config { return createDefaultConfig().(*Config) }

func TestReceiverEmitsAtStartAndOnEachInterval(t *testing.T) {
	sink := new(consumertest.MetricsSink)
	cfg := testConfig()
	cfg.CollectionInterval = time.Second
	r := newReceiver(receivertest.NewNopSettings(NewFactory().Type()), cfg, sink, &fakeSessions{list: twoSessions()})

	require.NoError(t, r.Start(context.Background(), componenttest.NewNopHost()))
	assert.Eventually(t, func() bool { return len(sink.AllMetrics()) >= 2 }, 5*time.Second, 50*time.Millisecond)
	require.NoError(t, r.Shutdown(context.Background()))
}

func TestReceiverSendsNothingWithoutSessions(t *testing.T) {
	sink := new(consumertest.MetricsSink)
	src := &fakeSessions{err: errNoSessions}
	r := newReceiver(receivertest.NewNopSettings(NewFactory().Type()), testConfig(), sink, src)

	require.NoError(t, r.Start(context.Background(), componenttest.NewNopHost()))
	assert.Eventually(t, func() bool { return src.reads.Load() >= 1 }, 5*time.Second, 10*time.Millisecond)
	require.NoError(t, r.Shutdown(context.Background()))

	assert.Empty(t, sink.AllMetrics())
}

func TestReceiverSurvivesAConsumerError(t *testing.T) {
	sink := &failingSink{}
	r := newReceiver(receivertest.NewNopSettings(NewFactory().Type()), testConfig(), sink, &fakeSessions{list: twoSessions()})

	require.NoError(t, r.Start(context.Background(), componenttest.NewNopHost()))
	assert.Eventually(t, func() bool { return sink.calls.Load() >= 1 }, 5*time.Second, 20*time.Millisecond)
	require.NoError(t, r.Shutdown(context.Background()))
}

func TestReceiverShutdownWithoutStartIsClean(t *testing.T) {
	r := newReceiver(receivertest.NewNopSettings(NewFactory().Type()), testConfig(), consumertest.NewNop(), &fakeSessions{})

	require.NoError(t, r.Shutdown(context.Background()))
}

// The receiver reads sysfs in the collector process: no external command, no cgo.
func TestSourceImportsNoExecAndNoCgo(t *testing.T) {
	files, err := filepath.Glob("*.go")
	require.NoError(t, err)
	require.NotEmpty(t, files)

	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), name, nil, parser.ImportsOnly)
		require.NoError(t, err, name)
		for _, imp := range f.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			assert.NotEqual(t, "os/exec", path, name)
			assert.NotEqual(t, "C", path, name)
		}
	}
}
