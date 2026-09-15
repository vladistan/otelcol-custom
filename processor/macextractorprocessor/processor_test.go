// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package macextractorprocessor

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/processor/processortest"
)

var testProcessorType = component.MustNewType("macextractor")

func TestLogsProcessor_ExtractFromBody(t *testing.T) {
	cfg := &Config{
		TargetAttribute:    "client.mac",
		SearchBody:         true,
		AttributesToSearch: []string{}, // don't search attributes
		ExtractAll:         false,
	}

	sink := &consumertest.LogsSink{}
	processor := &logsProcessor{
		config:       cfg,
		logger:       processortest.NewNopSettings(testProcessorType).Logger,
		nextConsumer: sink,
	}

	logs := createTestLogsWithBody("DHCPACK on 192.0.2.1 to 10:d5:61:17:80:6b (hostname) via em0")

	err := processor.ConsumeLogs(context.Background(), logs)
	require.NoError(t, err)

	// Verify MAC was extracted to log record attributes
	require.Equal(t, 1, sink.LogRecordCount())
	resultLogs := sink.AllLogs()[0]
	lr := resultLogs.ResourceLogs().At(0).ScopeLogs().At(0).LogRecords().At(0)
	mac, found := lr.Attributes().Get("client.mac")
	assert.True(t, found)
	assert.Equal(t, "10:d5:61:17:80:6b", mac.Str())
}

func TestLogsProcessor_ExtractFromWhitelistedAttribute(t *testing.T) {
	cfg := &Config{
		TargetAttribute:    "client.mac",
		SearchBody:         false,
		AttributesToSearch: []string{"original_message"},
		ExtractAll:         false,
	}

	sink := &consumertest.LogsSink{}
	processor := &logsProcessor{
		config:       cfg,
		logger:       processortest.NewNopSettings(testProcessorType).Logger,
		nextConsumer: sink,
	}

	// Create logs with MAC in original_message (whitelisted) and event_payload (not whitelisted)
	logs := plog.NewLogs()
	rl := logs.ResourceLogs().AppendEmpty()
	sl := rl.ScopeLogs().AppendEmpty()
	lr := sl.LogRecords().AppendEmpty()
	lr.Attributes().PutStr("original_message", "Device MAC: aa:bb:cc:dd:ee:ff")
	lr.Attributes().PutStr("event_payload", `{"mac": "11:22:33:44:55:66"}`) // should be ignored

	err := processor.ConsumeLogs(context.Background(), logs)
	require.NoError(t, err)

	// Verify MAC was extracted only from whitelisted attribute
	require.Equal(t, 1, sink.LogRecordCount())
	resultLogs := sink.AllLogs()[0]
	resultLR := resultLogs.ResourceLogs().At(0).ScopeLogs().At(0).LogRecords().At(0)
	mac, found := resultLR.Attributes().Get("client.mac")
	assert.True(t, found)
	assert.Equal(t, "aa:bb:cc:dd:ee:ff", mac.Str())
}

func TestLogsProcessor_ExtractAll(t *testing.T) {
	cfg := &Config{
		TargetAttribute:    "client.mac",
		SearchBody:         true,
		AttributesToSearch: []string{},
		ExtractAll:         true,
	}

	sink := &consumertest.LogsSink{}
	processor := &logsProcessor{
		config:       cfg,
		logger:       processortest.NewNopSettings(testProcessorType).Logger,
		nextConsumer: sink,
	}

	logs := createTestLogsWithBody("From aa:bb:cc:dd:ee:ff to 11:22:33:44:55:66")

	err := processor.ConsumeLogs(context.Background(), logs)
	require.NoError(t, err)

	// Verify both MACs were extracted to log record attributes
	require.Equal(t, 1, sink.LogRecordCount())
	resultLogs := sink.AllLogs()[0]
	lr := resultLogs.ResourceLogs().At(0).ScopeLogs().At(0).LogRecords().At(0)
	mac, found := lr.Attributes().Get("client.mac")
	assert.True(t, found)
	assert.Equal(t, "aa:bb:cc:dd:ee:ff,11:22:33:44:55:66", mac.Str())
}

func TestLogsProcessor_NoMAC(t *testing.T) {
	cfg := &Config{
		TargetAttribute:    "client.mac",
		SearchBody:         true,
		AttributesToSearch: []string{"original_message"},
		ExtractAll:         false,
	}

	sink := &consumertest.LogsSink{}
	processor := &logsProcessor{
		config:       cfg,
		logger:       processortest.NewNopSettings(testProcessorType).Logger,
		nextConsumer: sink,
	}

	logs := createTestLogsWithBody("This log has no MAC address")

	err := processor.ConsumeLogs(context.Background(), logs)
	require.NoError(t, err)

	// Verify no MAC attribute was added to log record
	require.Equal(t, 1, sink.LogRecordCount())
	resultLogs := sink.AllLogs()[0]
	lr := resultLogs.ResourceLogs().At(0).ScopeLogs().At(0).LogRecords().At(0)
	_, found := lr.Attributes().Get("client.mac")
	assert.False(t, found)
}

func TestLogsProcessor_CiscoFormat(t *testing.T) {
	cfg := &Config{
		TargetAttribute:    "client.mac",
		SearchBody:         true,
		AttributesToSearch: []string{},
		ExtractAll:         false,
	}

	sink := &consumertest.LogsSink{}
	processor := &logsProcessor{
		config:       cfg,
		logger:       processortest.NewNopSettings(testProcessorType).Logger,
		nextConsumer: sink,
	}

	logs := createTestLogsWithBody("%PORT_SECURITY-2-PSECURE_VIOLATION: Security violation, MAC address aabb.ccdd.1122")

	err := processor.ConsumeLogs(context.Background(), logs)
	require.NoError(t, err)

	// Verify Cisco format MAC was extracted and normalized
	require.Equal(t, 1, sink.LogRecordCount())
	resultLogs := sink.AllLogs()[0]
	lr := resultLogs.ResourceLogs().At(0).ScopeLogs().At(0).LogRecords().At(0)
	mac, found := lr.Attributes().Get("client.mac")
	assert.True(t, found)
	assert.Equal(t, "aa:bb:cc:dd:11:22", mac.Str())
}

func TestLogsProcessor_OnlySearchesWhitelistedAttributes(t *testing.T) {
	cfg := &Config{
		TargetAttribute:    "client.mac",
		SearchBody:         false,
		AttributesToSearch: []string{"original_message"},
		ExtractAll:         false,
	}

	sink := &consumertest.LogsSink{}
	processor := &logsProcessor{
		config:       cfg,
		logger:       processortest.NewNopSettings(testProcessorType).Logger,
		nextConsumer: sink,
	}

	// Create logs with MAC in original_message but not in other_attr
	logs := plog.NewLogs()
	rl := logs.ResourceLogs().AppendEmpty()
	sl := rl.ScopeLogs().AppendEmpty()
	lr := sl.LogRecords().AppendEmpty()
	lr.Attributes().PutStr("original_message", "MAC: aa:bb:cc:dd:ee:ff")
	lr.Attributes().PutStr("other_attr", "MAC: 11:22:33:44:55:66")

	err := processor.ConsumeLogs(context.Background(), logs)
	require.NoError(t, err)

	// Only the MAC from original_message should be extracted
	resultLogs := sink.AllLogs()[0]
	resultLR := resultLogs.ResourceLogs().At(0).ScopeLogs().At(0).LogRecords().At(0)
	mac, found := resultLR.Attributes().Get("client.mac")
	assert.True(t, found)
	assert.Equal(t, "aa:bb:cc:dd:ee:ff", mac.Str())
}

// Test: body has no MAC, other attrs have MACs but they're not whitelisted
// This is the bug fix test - no spurious client.mac should be set
func TestLogsProcessor_NoMACInBodyIgnoresOtherAttrs(t *testing.T) {
	cfg := &Config{
		TargetAttribute:    "client.mac",
		SearchBody:         true,
		AttributesToSearch: []string{"original_message"}, // only search this attr
		ExtractAll:         false,
	}

	sink := &consumertest.LogsSink{}
	processor := &logsProcessor{
		config:       cfg,
		logger:       processortest.NewNopSettings(testProcessorType).Logger,
		nextConsumer: sink,
	}

	logs := plog.NewLogs()
	rl := logs.ResourceLogs().AppendEmpty()
	sl := rl.ScopeLogs().AppendEmpty()
	lr := sl.LogRecords().AppendEmpty()
	lr.Body().SetStr("Claude session event: PostToolUse") // no MAC in body
	lr.Attributes().PutStr("event_payload", `{"tool_response": {"stdout": "- mac: \"c4:4f:33:91:a6:58\""}}`)
	// no original_message attribute - should not find any MAC

	err := processor.ConsumeLogs(context.Background(), logs)
	require.NoError(t, err)

	// No MAC should be extracted - body has none, event_payload is not whitelisted
	resultLogs := sink.AllLogs()[0]
	resultLR := resultLogs.ResourceLogs().At(0).ScopeLogs().At(0).LogRecords().At(0)
	_, found := resultLR.Attributes().Get("client.mac")
	assert.False(t, found, "client.mac should not be set when body has no MAC and other attrs are not whitelisted")
}

func TestLogsProcessor_SliceBody(t *testing.T) {
	cfg := &Config{
		TargetAttribute:    "client.mac",
		SearchBody:         true,
		AttributesToSearch: []string{},
		ExtractAll:         false,
	}

	sink := &consumertest.LogsSink{}
	processor := &logsProcessor{
		config:       cfg,
		logger:       processortest.NewNopSettings(testProcessorType).Logger,
		nextConsumer: sink,
	}

	// Create logs with slice body containing MAC
	logs := plog.NewLogs()
	rl := logs.ResourceLogs().AppendEmpty()
	sl := rl.ScopeLogs().AppendEmpty()
	lr := sl.LogRecords().AppendEmpty()
	bodySlice := lr.Body().SetEmptySlice()
	bodySlice.AppendEmpty().SetStr("first line")
	bodySlice.AppendEmpty().SetStr("DHCPACK to aa:bb:cc:dd:ee:ff")
	bodySlice.AppendEmpty().SetStr("third line")

	err := processor.ConsumeLogs(context.Background(), logs)
	require.NoError(t, err)

	// MAC should be extracted from slice element
	resultLogs := sink.AllLogs()[0]
	resultLR := resultLogs.ResourceLogs().At(0).ScopeLogs().At(0).LogRecords().At(0)
	mac, found := resultLR.Attributes().Get("client.mac")
	assert.True(t, found)
	assert.Equal(t, "aa:bb:cc:dd:ee:ff", mac.Str())
}

func TestLogsProcessor_MapBody(t *testing.T) {
	cfg := &Config{
		TargetAttribute:    "client.mac",
		SearchBody:         true,
		AttributesToSearch: []string{},
		ExtractAll:         false,
	}

	sink := &consumertest.LogsSink{}
	processor := &logsProcessor{
		config:       cfg,
		logger:       processortest.NewNopSettings(testProcessorType).Logger,
		nextConsumer: sink,
	}

	// Create logs with map body containing MAC (like journald logs)
	logs := plog.NewLogs()
	rl := logs.ResourceLogs().AppendEmpty()
	sl := rl.ScopeLogs().AppendEmpty()
	lr := sl.LogRecords().AppendEmpty()
	bodyMap := lr.Body().SetEmptyMap()
	bodyMap.PutStr("MESSAGE", "DHCPACK to aa:bb:cc:dd:ee:ff")
	bodyMap.PutStr("_HOSTNAME", "server1")

	err := processor.ConsumeLogs(context.Background(), logs)
	require.NoError(t, err)

	// MAC should be extracted from nested map
	resultLogs := sink.AllLogs()[0]
	resultLR := resultLogs.ResourceLogs().At(0).ScopeLogs().At(0).LogRecords().At(0)
	mac, found := resultLR.Attributes().Get("client.mac")
	assert.True(t, found)
	assert.Equal(t, "aa:bb:cc:dd:ee:ff", mac.Str())
}

func TestLogsProcessor_CustomTargetAttribute(t *testing.T) {
	cfg := &Config{
		TargetAttribute:    "device.mac_address",
		SearchBody:         true,
		AttributesToSearch: []string{},
		ExtractAll:         false,
	}

	sink := &consumertest.LogsSink{}
	processor := &logsProcessor{
		config:       cfg,
		logger:       processortest.NewNopSettings(testProcessorType).Logger,
		nextConsumer: sink,
	}

	logs := createTestLogsWithBody("Device MAC: aa:bb:cc:dd:ee:ff")

	err := processor.ConsumeLogs(context.Background(), logs)
	require.NoError(t, err)

	// Verify MAC was set to custom attribute on log record
	resultLogs := sink.AllLogs()[0]
	lr := resultLogs.ResourceLogs().At(0).ScopeLogs().At(0).LogRecords().At(0)
	mac, found := lr.Attributes().Get("device.mac_address")
	assert.True(t, found)
	assert.Equal(t, "aa:bb:cc:dd:ee:ff", mac.Str())
}

// Test: Missing whitelisted attribute should not crash
func TestLogsProcessor_MissingAttribute(t *testing.T) {
	cfg := &Config{
		TargetAttribute:    "client.mac",
		SearchBody:         false,
		AttributesToSearch: []string{"original_message", "nonexistent_attr"},
		ExtractAll:         false,
	}

	sink := &consumertest.LogsSink{}
	processor := &logsProcessor{
		config:       cfg,
		logger:       processortest.NewNopSettings(testProcessorType).Logger,
		nextConsumer: sink,
	}

	// Create logs without the whitelisted attribute
	logs := plog.NewLogs()
	rl := logs.ResourceLogs().AppendEmpty()
	sl := rl.ScopeLogs().AppendEmpty()
	lr := sl.LogRecords().AppendEmpty()
	lr.Body().SetStr("some body without MAC")
	lr.Attributes().PutStr("other_attr", "MAC: aa:bb:cc:dd:ee:ff") // not in whitelist

	err := processor.ConsumeLogs(context.Background(), logs)
	require.NoError(t, err)

	// Should not crash, no MAC extracted
	resultLogs := sink.AllLogs()[0]
	resultLR := resultLogs.ResourceLogs().At(0).ScopeLogs().At(0).LogRecords().At(0)
	_, found := resultLR.Attributes().Get("client.mac")
	assert.False(t, found, "client.mac should not be set when whitelisted attr is missing")
}

func TestFactory_CreateDefaultConfig(t *testing.T) {
	factory := NewFactory()
	cfg := factory.CreateDefaultConfig()

	assert.NotNil(t, cfg)
	processorCfg, ok := cfg.(*Config)
	assert.True(t, ok)
	assert.Equal(t, "client.mac", processorCfg.TargetAttribute)
	assert.True(t, processorCfg.SearchBody)
	assert.Equal(t, []string{"original_message"}, processorCfg.AttributesToSearch)
	assert.False(t, processorCfg.ExtractAll)
}

func TestFactory_CreateLogsProcessor(t *testing.T) {
	factory := NewFactory()
	cfg := factory.CreateDefaultConfig()

	sink := &consumertest.LogsSink{}
	processor, err := factory.CreateLogs(
		context.Background(),
		processortest.NewNopSettings(testProcessorType),
		cfg,
		sink,
	)

	require.NoError(t, err)
	assert.NotNil(t, processor)
}

func TestLogsProcessor_MultipleLogRecords(t *testing.T) {
	cfg := &Config{
		TargetAttribute:    "client.mac",
		SearchBody:         true,
		AttributesToSearch: []string{},
		ExtractAll:         false,
	}

	sink := &consumertest.LogsSink{}
	processor := &logsProcessor{
		config:       cfg,
		logger:       processortest.NewNopSettings(testProcessorType).Logger,
		nextConsumer: sink,
	}

	// Create logs with multiple records - some with MAC, some without
	logs := plog.NewLogs()
	rl := logs.ResourceLogs().AppendEmpty()
	sl := rl.ScopeLogs().AppendEmpty()

	lr1 := sl.LogRecords().AppendEmpty()
	lr1.Body().SetStr("DHCPACK to aa:bb:cc:dd:ee:ff")

	lr2 := sl.LogRecords().AppendEmpty()
	lr2.Body().SetStr("No MAC here")

	lr3 := sl.LogRecords().AppendEmpty()
	lr3.Body().SetStr("DHCPREQUEST from 11:22:33:44:55:66")

	err := processor.ConsumeLogs(context.Background(), logs)
	require.NoError(t, err)

	// Each log record should be processed independently
	require.Equal(t, 3, sink.LogRecordCount())
	resultLogs := sink.AllLogs()[0]
	logRecords := resultLogs.ResourceLogs().At(0).ScopeLogs().At(0).LogRecords()

	// lr1 has MAC
	mac1, found1 := logRecords.At(0).Attributes().Get("client.mac")
	assert.True(t, found1, "lr1 should have MAC")
	assert.Equal(t, "aa:bb:cc:dd:ee:ff", mac1.Str())

	// lr2 has NO MAC - this is the key test for the bug fix
	_, found2 := logRecords.At(1).Attributes().Get("client.mac")
	assert.False(t, found2, "lr2 should NOT have MAC (no false positives)")

	// lr3 has MAC
	mac3, found3 := logRecords.At(2).Attributes().Get("client.mac")
	assert.True(t, found3, "lr3 should have MAC")
	assert.Equal(t, "11:22:33:44:55:66", mac3.Str())
}

func TestLogsProcessor_Capabilities(t *testing.T) {
	cfg := createDefaultConfig().(*Config)
	processor := &logsProcessor{
		config: cfg,
	}

	caps := processor.Capabilities()
	assert.True(t, caps.MutatesData)
}

func TestLogsProcessor_StartShutdown(t *testing.T) {
	cfg := createDefaultConfig().(*Config)
	sink := &consumertest.LogsSink{}
	processor := &logsProcessor{
		config:       cfg,
		logger:       processortest.NewNopSettings(testProcessorType).Logger,
		nextConsumer: sink,
	}

	err := processor.Start(context.Background(), nil)
	assert.NoError(t, err)

	err = processor.Shutdown(context.Background())
	assert.NoError(t, err)
}

// Helper functions

func createTestLogsWithBody(body string) plog.Logs {
	logs := plog.NewLogs()
	rl := logs.ResourceLogs().AppendEmpty()
	sl := rl.ScopeLogs().AppendEmpty()
	lr := sl.LogRecords().AppendEmpty()
	lr.Body().SetStr(body)
	return logs
}
