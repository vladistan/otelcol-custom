// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package assetdbprocessor

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"go.opentelemetry.io/collector/processor/processortest"
)

var testProcessorType = component.MustNewType("assetdb")

const testAssetYAML = `assets:
  - mac: "aa:bb:cc:dd:ee:ff"
    hostname: "switch-core-01"
    location: "dc1-rack-a1"
  - mac: "11:22:33:44:55:66"
    hostname: "ap-office-lobby"
    location: "office-floor2"
  - mac: "de:ad:be:ef:ca:fe"
    hostname: "router-edge-01"
    location: "dc1-rack-b2"
`

func createTestAssetFile(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "assets.yaml")
	err := os.WriteFile(path, []byte(testAssetYAML), 0644)
	require.NoError(t, err)
	return path
}

func TestAssetDBProcessor_LookupSuccess(t *testing.T) {
	// Create test asset file
	assetPath := createTestAssetFile(t)

	// Create test logs with MAC address in log record attributes
	ld := plog.NewLogs()
	rl := ld.ResourceLogs().AppendEmpty()
	sl := rl.ScopeLogs().AppendEmpty()
	lr := sl.LogRecords().AppendEmpty()
	lr.Body().SetStr("Test log message")
	lr.Attributes().PutStr("client.mac", "aa:bb:cc:dd:ee:ff")

	// Create processor
	sink := &consumertest.LogsSink{}
	settings := processortest.NewNopSettings(testProcessorType)
	cfg := &Config{
		DatabasePath:    assetPath,
		SourceAttribute: "client.mac",
	}

	proc, err := newLogsProcessor(settings, cfg, sink)
	require.NoError(t, err)

	// Start processor (loads database)
	err = proc.Start(context.Background(), nil)
	require.NoError(t, err)

	// Process logs
	err = proc.ConsumeLogs(context.Background(), ld)
	require.NoError(t, err)

	// Verify enrichment
	require.Equal(t, 1, sink.LogRecordCount())
	outputLD := sink.AllLogs()[0]
	attrs := outputLD.ResourceLogs().At(0).ScopeLogs().At(0).LogRecords().At(0).Attributes()

	// Check enriched attributes
	hostName, ok := attrs.Get("client.hostname")
	assert.True(t, ok, "Expected client.hostname attribute")
	assert.Equal(t, "switch-core-01", hostName.Str())

	location, ok := attrs.Get("host.geo.description")
	assert.True(t, ok, "Expected host.geo.description attribute")
	assert.Equal(t, "dc1-rack-a1", location.Str())

	// Shutdown processor
	err = proc.Shutdown(context.Background())
	require.NoError(t, err)
}

func TestAssetDBProcessor_LookupNotFound(t *testing.T) {
	// Create test asset file
	assetPath := createTestAssetFile(t)

	// Create test logs with unknown MAC address
	ld := plog.NewLogs()
	rl := ld.ResourceLogs().AppendEmpty()
	rl.Resource().Attributes().PutStr("client.mac", "ff:ff:ff:ff:ff:ff")
	sl := rl.ScopeLogs().AppendEmpty()
	lr := sl.LogRecords().AppendEmpty()
	lr.Body().SetStr("Test log message")

	// Create processor
	sink := &consumertest.LogsSink{}
	settings := processortest.NewNopSettings(testProcessorType)
	cfg := &Config{
		DatabasePath:    assetPath,
		SourceAttribute: "client.mac",
	}

	proc, err := newLogsProcessor(settings, cfg, sink)
	require.NoError(t, err)
	err = proc.Start(context.Background(), nil)
	require.NoError(t, err)

	// Process logs
	err = proc.ConsumeLogs(context.Background(), ld)
	require.NoError(t, err)

	// Verify no enrichment (silently pass through)
	require.Equal(t, 1, sink.LogRecordCount())
	outputLD := sink.AllLogs()[0]
	attrs := outputLD.ResourceLogs().At(0).Resource().Attributes()

	// Should NOT have enriched attributes
	_, ok := attrs.Get("client.hostname")
	assert.False(t, ok, "Should not have client.hostname for unknown MAC")

	_, ok = attrs.Get("host.geo.description")
	assert.False(t, ok, "Should not have host.geo.description for unknown MAC")

	err = proc.Shutdown(context.Background())
	require.NoError(t, err)
}

func TestAssetDBProcessor_MACNormalization(t *testing.T) {
	// Create test asset file
	assetPath := createTestAssetFile(t)

	testCases := []struct {
		name     string
		inputMAC string
		wantHost string
	}{
		{"lowercase_colons", "aa:bb:cc:dd:ee:ff", "switch-core-01"},
		{"uppercase_colons", "AA:BB:CC:DD:EE:FF", "switch-core-01"},
		{"lowercase_dashes", "aa-bb-cc-dd-ee-ff", "switch-core-01"},
		{"uppercase_dashes", "AA-BB-CC-DD-EE-FF", "switch-core-01"},
		{"no_separators", "aabbccddeeff", "switch-core-01"},
		{"uppercase_no_sep", "AABBCCDDEEFF", "switch-core-01"},
		{"mixed_case", "Aa:Bb:Cc:Dd:Ee:Ff", "switch-core-01"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create test logs with MAC in log record attributes
			ld := plog.NewLogs()
			rl := ld.ResourceLogs().AppendEmpty()
			sl := rl.ScopeLogs().AppendEmpty()
			lr := sl.LogRecords().AppendEmpty()
			lr.Body().SetStr("Test log message")
			lr.Attributes().PutStr("client.mac", tc.inputMAC)

			// Create processor
			sink := &consumertest.LogsSink{}
			settings := processortest.NewNopSettings(testProcessorType)
			cfg := &Config{
				DatabasePath:    assetPath,
				SourceAttribute: "client.mac",
			}

			proc, err := newLogsProcessor(settings, cfg, sink)
			require.NoError(t, err)
			err = proc.Start(context.Background(), nil)
			require.NoError(t, err)

			// Process logs
			err = proc.ConsumeLogs(context.Background(), ld)
			require.NoError(t, err)

			// Verify enrichment in log record attributes
			require.Equal(t, 1, sink.LogRecordCount())
			outputLD := sink.AllLogs()[0]
			attrs := outputLD.ResourceLogs().At(0).ScopeLogs().At(0).LogRecords().At(0).Attributes()

			hostName, ok := attrs.Get("client.hostname")
			assert.True(t, ok, "Expected client.hostname attribute for MAC: %s", tc.inputMAC)
			assert.Equal(t, tc.wantHost, hostName.Str())

			err = proc.Shutdown(context.Background())
			require.NoError(t, err)
		})
	}
}

func TestAssetDBProcessor_NoSourceAttribute(t *testing.T) {
	// Create test asset file
	assetPath := createTestAssetFile(t)

	// Create test logs WITHOUT MAC address attribute
	ld := plog.NewLogs()
	rl := ld.ResourceLogs().AppendEmpty()
	rl.Resource().Attributes().PutStr("host.name", "some-host")
	sl := rl.ScopeLogs().AppendEmpty()
	lr := sl.LogRecords().AppendEmpty()
	lr.Body().SetStr("Test log message")

	// Create processor
	sink := &consumertest.LogsSink{}
	settings := processortest.NewNopSettings(testProcessorType)
	cfg := &Config{
		DatabasePath:    assetPath,
		SourceAttribute: "client.mac",
	}

	proc, err := newLogsProcessor(settings, cfg, sink)
	require.NoError(t, err)
	err = proc.Start(context.Background(), nil)
	require.NoError(t, err)

	// Process logs - should pass through without error
	err = proc.ConsumeLogs(context.Background(), ld)
	require.NoError(t, err)

	// Verify passthrough
	require.Equal(t, 1, sink.LogRecordCount())

	err = proc.Shutdown(context.Background())
	require.NoError(t, err)
}

func TestAssetDBProcessor_InvalidDatabasePath(t *testing.T) {
	// Create processor with non-existent path
	sink := &consumertest.LogsSink{}
	settings := processortest.NewNopSettings(testProcessorType)
	cfg := &Config{
		DatabasePath:    "/non/existent/path/assets.yaml",
		SourceAttribute: "client.mac",
	}

	proc, err := newLogsProcessor(settings, cfg, sink)
	require.NoError(t, err)

	// Start should fail
	err = proc.Start(context.Background(), nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to load asset database")
}

func TestConfig_Validate(t *testing.T) {
	testCases := []struct {
		name    string
		cfg     *Config
		wantErr bool
	}{
		{
			name: "valid_config",
			cfg: &Config{
				DatabasePath:    "/path/to/assets.yaml",
				SourceAttribute: "client.mac",
			},
			wantErr: false,
		},
		{
			name: "empty_database_path",
			cfg: &Config{
				DatabasePath:    "",
				SourceAttribute: "client.mac",
			},
			wantErr: true,
		},
		{
			name: "default_source_attribute",
			cfg: &Config{
				DatabasePath:    "/path/to/assets.yaml",
				SourceAttribute: "",
			},
			wantErr: false, // Empty source attribute uses default
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate()
			if tc.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestNormalizeMAC(t *testing.T) {
	testCases := []struct {
		input string
		want  string
	}{
		{"aa:bb:cc:dd:ee:ff", "aa:bb:cc:dd:ee:ff"},
		{"AA:BB:CC:DD:EE:FF", "aa:bb:cc:dd:ee:ff"},
		{"aa-bb-cc-dd-ee-ff", "aa:bb:cc:dd:ee:ff"},
		{"AA-BB-CC-DD-EE-FF", "aa:bb:cc:dd:ee:ff"},
		{"aabbccddeeff", "aa:bb:cc:dd:ee:ff"},
		{"AABBCCDDEEFF", "aa:bb:cc:dd:ee:ff"},
		{"Aa:Bb:Cc:Dd:Ee:Ff", "aa:bb:cc:dd:ee:ff"},
		// Edge cases
		{"", ""},
		{"invalid", "invalid"},
		{"aa:bb", "aa:bb"},
	}

	for _, tc := range testCases {
		t.Run(tc.input, func(t *testing.T) {
			got := normalizeMAC(tc.input)
			assert.Equal(t, tc.want, got)
		})
	}
}

// =============================================================================
// Metrics Processor Tests
// =============================================================================

func TestMetricsProcessor_LookupSuccess(t *testing.T) {
	assetPath := createTestAssetFile(t)

	// Create test metrics with MAC address
	md := pmetric.NewMetrics()
	rm := md.ResourceMetrics().AppendEmpty()
	rm.Resource().Attributes().PutStr("client.mac", "aa:bb:cc:dd:ee:ff")
	sm := rm.ScopeMetrics().AppendEmpty()
	m := sm.Metrics().AppendEmpty()
	m.SetName("test.metric")
	m.SetEmptyGauge().DataPoints().AppendEmpty().SetDoubleValue(42.0)

	// Create processor
	sink := &consumertest.MetricsSink{}
	settings := processortest.NewNopSettings(testProcessorType)
	cfg := &Config{
		DatabasePath:    assetPath,
		SourceAttribute: "client.mac",
	}

	proc, err := newMetricsProcessor(settings, cfg, sink)
	require.NoError(t, err)

	// Verify capabilities
	assert.True(t, proc.Capabilities().MutatesData)

	// Start processor
	err = proc.Start(context.Background(), nil)
	require.NoError(t, err)

	// Process metrics
	err = proc.ConsumeMetrics(context.Background(), md)
	require.NoError(t, err)

	// Verify enrichment
	require.Equal(t, 1, sink.DataPointCount())
	outputMD := sink.AllMetrics()[0]
	attrs := outputMD.ResourceMetrics().At(0).Resource().Attributes()

	hostName, ok := attrs.Get("client.hostname")
	assert.True(t, ok, "Expected client.hostname attribute")
	assert.Equal(t, "switch-core-01", hostName.Str())

	location, ok := attrs.Get("host.geo.description")
	assert.True(t, ok, "Expected host.geo.description attribute")
	assert.Equal(t, "dc1-rack-a1", location.Str())

	// Shutdown
	err = proc.Shutdown(context.Background())
	require.NoError(t, err)
}

func TestMetricsProcessor_LookupNotFound(t *testing.T) {
	assetPath := createTestAssetFile(t)

	// Create test metrics with unknown MAC
	md := pmetric.NewMetrics()
	rm := md.ResourceMetrics().AppendEmpty()
	rm.Resource().Attributes().PutStr("client.mac", "ff:ff:ff:ff:ff:ff")
	sm := rm.ScopeMetrics().AppendEmpty()
	m := sm.Metrics().AppendEmpty()
	m.SetName("test.metric")
	m.SetEmptyGauge().DataPoints().AppendEmpty().SetDoubleValue(42.0)

	// Create processor
	sink := &consumertest.MetricsSink{}
	settings := processortest.NewNopSettings(testProcessorType)
	cfg := &Config{
		DatabasePath:    assetPath,
		SourceAttribute: "client.mac",
	}

	proc, err := newMetricsProcessor(settings, cfg, sink)
	require.NoError(t, err)
	err = proc.Start(context.Background(), nil)
	require.NoError(t, err)

	// Process metrics
	err = proc.ConsumeMetrics(context.Background(), md)
	require.NoError(t, err)

	// Verify no enrichment
	outputMD := sink.AllMetrics()[0]
	attrs := outputMD.ResourceMetrics().At(0).Resource().Attributes()

	_, ok := attrs.Get("client.hostname")
	assert.False(t, ok, "Should not have client.hostname for unknown MAC")

	err = proc.Shutdown(context.Background())
	require.NoError(t, err)
}

func TestMetricsProcessor_NoSourceAttribute(t *testing.T) {
	assetPath := createTestAssetFile(t)

	// Create test metrics WITHOUT MAC address
	md := pmetric.NewMetrics()
	rm := md.ResourceMetrics().AppendEmpty()
	rm.Resource().Attributes().PutStr("other.attr", "value")
	sm := rm.ScopeMetrics().AppendEmpty()
	m := sm.Metrics().AppendEmpty()
	m.SetName("test.metric")
	m.SetEmptyGauge().DataPoints().AppendEmpty().SetDoubleValue(42.0)

	// Create processor
	sink := &consumertest.MetricsSink{}
	settings := processortest.NewNopSettings(testProcessorType)
	cfg := &Config{
		DatabasePath:    assetPath,
		SourceAttribute: "client.mac",
	}

	proc, err := newMetricsProcessor(settings, cfg, sink)
	require.NoError(t, err)
	err = proc.Start(context.Background(), nil)
	require.NoError(t, err)

	// Process metrics - should pass through without error
	err = proc.ConsumeMetrics(context.Background(), md)
	require.NoError(t, err)

	// Verify passthrough
	require.Equal(t, 1, sink.DataPointCount())

	err = proc.Shutdown(context.Background())
	require.NoError(t, err)
}

func TestMetricsProcessor_InvalidDatabasePath(t *testing.T) {
	sink := &consumertest.MetricsSink{}
	settings := processortest.NewNopSettings(testProcessorType)
	cfg := &Config{
		DatabasePath:    "/non/existent/path/assets.yaml",
		SourceAttribute: "client.mac",
	}

	proc, err := newMetricsProcessor(settings, cfg, sink)
	require.NoError(t, err)

	err = proc.Start(context.Background(), nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to load asset database")
}

// =============================================================================
// Traces Processor Tests
// =============================================================================

func TestTracesProcessor_LookupSuccess(t *testing.T) {
	assetPath := createTestAssetFile(t)

	// Create test traces with MAC address
	td := ptrace.NewTraces()
	rs := td.ResourceSpans().AppendEmpty()
	rs.Resource().Attributes().PutStr("client.mac", "11:22:33:44:55:66")
	ss := rs.ScopeSpans().AppendEmpty()
	span := ss.Spans().AppendEmpty()
	span.SetName("test-span")

	// Create processor
	sink := &consumertest.TracesSink{}
	settings := processortest.NewNopSettings(testProcessorType)
	cfg := &Config{
		DatabasePath:    assetPath,
		SourceAttribute: "client.mac",
	}

	proc, err := newTracesProcessor(settings, cfg, sink)
	require.NoError(t, err)

	// Verify capabilities
	assert.True(t, proc.Capabilities().MutatesData)

	// Start processor
	err = proc.Start(context.Background(), nil)
	require.NoError(t, err)

	// Process traces
	err = proc.ConsumeTraces(context.Background(), td)
	require.NoError(t, err)

	// Verify enrichment
	require.Equal(t, 1, sink.SpanCount())
	outputTD := sink.AllTraces()[0]
	attrs := outputTD.ResourceSpans().At(0).Resource().Attributes()

	hostName, ok := attrs.Get("client.hostname")
	assert.True(t, ok, "Expected client.hostname attribute")
	assert.Equal(t, "ap-office-lobby", hostName.Str())

	location, ok := attrs.Get("host.geo.description")
	assert.True(t, ok, "Expected host.geo.description attribute")
	assert.Equal(t, "office-floor2", location.Str())

	// Shutdown
	err = proc.Shutdown(context.Background())
	require.NoError(t, err)
}

func TestTracesProcessor_LookupNotFound(t *testing.T) {
	assetPath := createTestAssetFile(t)

	// Create test traces with unknown MAC
	td := ptrace.NewTraces()
	rs := td.ResourceSpans().AppendEmpty()
	rs.Resource().Attributes().PutStr("client.mac", "ff:ff:ff:ff:ff:ff")
	ss := rs.ScopeSpans().AppendEmpty()
	span := ss.Spans().AppendEmpty()
	span.SetName("test-span")

	// Create processor
	sink := &consumertest.TracesSink{}
	settings := processortest.NewNopSettings(testProcessorType)
	cfg := &Config{
		DatabasePath:    assetPath,
		SourceAttribute: "client.mac",
	}

	proc, err := newTracesProcessor(settings, cfg, sink)
	require.NoError(t, err)
	err = proc.Start(context.Background(), nil)
	require.NoError(t, err)

	// Process traces
	err = proc.ConsumeTraces(context.Background(), td)
	require.NoError(t, err)

	// Verify no enrichment
	outputTD := sink.AllTraces()[0]
	attrs := outputTD.ResourceSpans().At(0).Resource().Attributes()

	_, ok := attrs.Get("client.hostname")
	assert.False(t, ok, "Should not have client.hostname for unknown MAC")

	err = proc.Shutdown(context.Background())
	require.NoError(t, err)
}

func TestTracesProcessor_NoSourceAttribute(t *testing.T) {
	assetPath := createTestAssetFile(t)

	// Create test traces WITHOUT MAC address
	td := ptrace.NewTraces()
	rs := td.ResourceSpans().AppendEmpty()
	rs.Resource().Attributes().PutStr("other.attr", "value")
	ss := rs.ScopeSpans().AppendEmpty()
	span := ss.Spans().AppendEmpty()
	span.SetName("test-span")

	// Create processor
	sink := &consumertest.TracesSink{}
	settings := processortest.NewNopSettings(testProcessorType)
	cfg := &Config{
		DatabasePath:    assetPath,
		SourceAttribute: "client.mac",
	}

	proc, err := newTracesProcessor(settings, cfg, sink)
	require.NoError(t, err)
	err = proc.Start(context.Background(), nil)
	require.NoError(t, err)

	// Process traces - should pass through without error
	err = proc.ConsumeTraces(context.Background(), td)
	require.NoError(t, err)

	// Verify passthrough
	require.Equal(t, 1, sink.SpanCount())

	err = proc.Shutdown(context.Background())
	require.NoError(t, err)
}

func TestTracesProcessor_InvalidDatabasePath(t *testing.T) {
	sink := &consumertest.TracesSink{}
	settings := processortest.NewNopSettings(testProcessorType)
	cfg := &Config{
		DatabasePath:    "/non/existent/path/assets.yaml",
		SourceAttribute: "client.mac",
	}

	proc, err := newTracesProcessor(settings, cfg, sink)
	require.NoError(t, err)

	err = proc.Start(context.Background(), nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to load asset database")
}

// =============================================================================
// Factory Tests
// =============================================================================

func TestNewFactory(t *testing.T) {
	factory := NewFactory()
	assert.NotNil(t, factory)
	assert.Equal(t, "assetdb", factory.Type().String())
}

func TestCreateDefaultConfig(t *testing.T) {
	factory := NewFactory()
	cfg := factory.CreateDefaultConfig()
	assert.NotNil(t, cfg)

	assetCfg, ok := cfg.(*Config)
	require.True(t, ok)
	assert.Equal(t, "client.mac", assetCfg.SourceAttribute)
	assert.Empty(t, assetCfg.DatabasePath)
}

func TestFactory_CreateLogsProcessor(t *testing.T) {
	assetPath := createTestAssetFile(t)

	factory := NewFactory()
	cfg := &Config{
		DatabasePath:    assetPath,
		SourceAttribute: "client.mac",
	}

	sink := &consumertest.LogsSink{}
	settings := processortest.NewNopSettings(testProcessorType)

	proc, err := factory.CreateLogs(context.Background(), settings, cfg, sink)
	require.NoError(t, err)
	assert.NotNil(t, proc)
}

func TestFactory_CreateMetricsProcessor(t *testing.T) {
	assetPath := createTestAssetFile(t)

	factory := NewFactory()
	cfg := &Config{
		DatabasePath:    assetPath,
		SourceAttribute: "client.mac",
	}

	sink := &consumertest.MetricsSink{}
	settings := processortest.NewNopSettings(testProcessorType)

	proc, err := factory.CreateMetrics(context.Background(), settings, cfg, sink)
	require.NoError(t, err)
	assert.NotNil(t, proc)
}

func TestFactory_CreateTracesProcessor(t *testing.T) {
	assetPath := createTestAssetFile(t)

	factory := NewFactory()
	cfg := &Config{
		DatabasePath:    assetPath,
		SourceAttribute: "client.mac",
	}

	sink := &consumertest.TracesSink{}
	settings := processortest.NewNopSettings(testProcessorType)

	proc, err := factory.CreateTraces(context.Background(), settings, cfg, sink)
	require.NoError(t, err)
	assert.NotNil(t, proc)
}

// =============================================================================
// Logs Processor Additional Tests (Capabilities)
// =============================================================================

func TestLogsProcessor_Capabilities(t *testing.T) {
	assetPath := createTestAssetFile(t)

	sink := &consumertest.LogsSink{}
	settings := processortest.NewNopSettings(testProcessorType)
	cfg := &Config{
		DatabasePath:    assetPath,
		SourceAttribute: "client.mac",
	}

	proc, err := newLogsProcessor(settings, cfg, sink)
	require.NoError(t, err)

	assert.True(t, proc.Capabilities().MutatesData)
}

// =============================================================================
// Edge Cases
// =============================================================================

func TestLogsProcessor_EmptyMACValue(t *testing.T) {
	assetPath := createTestAssetFile(t)

	// Create test logs with empty MAC address value
	ld := plog.NewLogs()
	rl := ld.ResourceLogs().AppendEmpty()
	rl.Resource().Attributes().PutStr("client.mac", "")
	sl := rl.ScopeLogs().AppendEmpty()
	lr := sl.LogRecords().AppendEmpty()
	lr.Body().SetStr("Test log message")

	sink := &consumertest.LogsSink{}
	settings := processortest.NewNopSettings(testProcessorType)
	cfg := &Config{
		DatabasePath:    assetPath,
		SourceAttribute: "client.mac",
	}

	proc, err := newLogsProcessor(settings, cfg, sink)
	require.NoError(t, err)
	err = proc.Start(context.Background(), nil)
	require.NoError(t, err)

	// Process logs - should pass through without error
	err = proc.ConsumeLogs(context.Background(), ld)
	require.NoError(t, err)

	// Verify no enrichment for empty MAC
	outputLD := sink.AllLogs()[0]
	attrs := outputLD.ResourceLogs().At(0).Resource().Attributes()

	_, ok := attrs.Get("client.hostname")
	assert.False(t, ok, "Should not have client.hostname for empty MAC")

	err = proc.Shutdown(context.Background())
	require.NoError(t, err)
}

func TestLogsProcessor_DefaultSourceAttribute(t *testing.T) {
	assetPath := createTestAssetFile(t)

	// Create test logs with MAC address in log record attributes
	ld := plog.NewLogs()
	rl := ld.ResourceLogs().AppendEmpty()
	sl := rl.ScopeLogs().AppendEmpty()
	lr := sl.LogRecords().AppendEmpty()
	lr.Body().SetStr("Test log message")
	lr.Attributes().PutStr("client.mac", "de:ad:be:ef:ca:fe")

	sink := &consumertest.LogsSink{}
	settings := processortest.NewNopSettings(testProcessorType)
	// Empty SourceAttribute should use default "client.mac"
	cfg := &Config{
		DatabasePath:    assetPath,
		SourceAttribute: "",
	}

	proc, err := newLogsProcessor(settings, cfg, sink)
	require.NoError(t, err)
	err = proc.Start(context.Background(), nil)
	require.NoError(t, err)

	err = proc.ConsumeLogs(context.Background(), ld)
	require.NoError(t, err)

	// Verify enrichment worked with default source attribute
	outputLD := sink.AllLogs()[0]
	attrs := outputLD.ResourceLogs().At(0).ScopeLogs().At(0).LogRecords().At(0).Attributes()

	hostName, ok := attrs.Get("client.hostname")
	assert.True(t, ok, "Expected client.hostname attribute")
	assert.Equal(t, "router-edge-01", hostName.Str())

	err = proc.Shutdown(context.Background())
	require.NoError(t, err)
}
