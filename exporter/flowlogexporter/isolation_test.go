// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package flowlogexporter

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component/componenttest"
)

// A connection that always fails keeps the flow exporter retrying for its whole retry time. The
// retry runs in the queue worker, so the caller returns at once and a second exporter that gets the
// same batch from the same caller is not held up.
func TestRetry_AnAlwaysFailingConnectionDoesNotDelayAnotherExporter(t *testing.T) {
	const retryLimit = 600 * time.Millisecond
	failCfg := testConfig()
	failCfg.BackOffConfig.InitialInterval = 30 * time.Millisecond
	failCfg.BackOffConfig.MaxInterval = 60 * time.Millisecond
	failCfg.BackOffConfig.MaxElapsedTime = retryLimit
	failing := &fakeConn{sendErr: errBoom}
	set, _, reader := rigSettings()
	failExporter, err := buildLogs(context.Background(), set, failCfg, func(context.Context) (conn, error) { return failing, nil })
	require.NoError(t, err)
	require.NoError(t, failExporter.Start(context.Background(), componenttest.NewNopHost()))
	t.Cleanup(func() { _ = failExporter.Shutdown(context.Background()) })

	fast := &fakeConn{}
	other, err := buildLogs(context.Background(), mustSettings(), testConfig(), func(context.Context) (conn, error) { return fast, nil })
	require.NoError(t, err)
	require.NoError(t, other.Start(context.Background(), componenttest.NewNopHost()))
	t.Cleanup(func() { _ = other.Shutdown(context.Background()) })

	// the failing exporter comes first in the fan-out, as in a pipeline
	begin := time.Now()
	require.NoError(t, failExporter.ConsumeLogs(context.Background(), logsOf(flowAttrs(nil))))
	require.NoError(t, other.ConsumeLogs(context.Background(), logsOf(flowAttrs(nil))))

	assert.Eventually(t, func() bool { return fast.sendCount() == 1 }, retryLimit/3, time.Millisecond, "the second exporter sent its batch")
	assert.Less(t, time.Since(begin), retryLimit/3, "well inside the retry time of the failing one")
	// the failing exporter did retry to its limit and then dropped the batch
	rig := &testRig{t: t, reader: reader}
	assert.Eventually(t, func() bool { return rig.counter("otelcol_flowlog_rows_dropped")[dropInsertFailed] == 1 }, 5*time.Second, 10*time.Millisecond)
	assert.Greater(t, failing.sendCount(), 2, "it retried more than once")
	assert.GreaterOrEqual(t, time.Since(begin), retryLimit-failCfg.BackOffConfig.MaxInterval, "the drop came at the end of the retry time")
}
