// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package ipextractorprocessor

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

var testProcessorType = component.MustNewType("ipextractor")

func TestLogsProcessor_ExtractFromBody(t *testing.T) {
	cfg := &Config{
		TargetAttribute:    "client.ip",
		SearchBody:         true,
		AttributesToSearch: []string{}, // don't search attributes
		ExtractAll:         false,
		ExtractIPv6:        false,
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

	// Verify IP was extracted to log record attributes
	require.Equal(t, 1, sink.LogRecordCount())
	resultLogs := sink.AllLogs()[0]
	lr := resultLogs.ResourceLogs().At(0).ScopeLogs().At(0).LogRecords().At(0)
	ip, found := lr.Attributes().Get("client.ip")
	assert.True(t, found)
	assert.Equal(t, "192.0.2.1", ip.Str())
}

func TestLogsProcessor_ExtractFromWhitelistedAttribute(t *testing.T) {
	cfg := &Config{
		TargetAttribute:    "client.ip",
		SearchBody:         false,
		AttributesToSearch: []string{"original_message"},
		ExtractAll:         false,
		ExtractIPv6:        false,
	}

	sink := &consumertest.LogsSink{}
	processor := &logsProcessor{
		config:       cfg,
		logger:       processortest.NewNopSettings(testProcessorType).Logger,
		nextConsumer: sink,
	}

	// Create logs with IP in original_message (whitelisted) and net.peer.ip (not whitelisted)
	logs := plog.NewLogs()
	rl := logs.ResourceLogs().AppendEmpty()
	sl := rl.ScopeLogs().AppendEmpty()
	lr := sl.LogRecords().AppendEmpty()
	lr.Attributes().PutStr("original_message", "Connection from 10.0.0.50:443")
	lr.Attributes().PutStr("net.peer.ip", "192.168.1.1") // should be ignored

	err := processor.ConsumeLogs(context.Background(), logs)
	require.NoError(t, err)

	// Verify IP was extracted only from whitelisted attribute
	require.Equal(t, 1, sink.LogRecordCount())
	resultLogs := sink.AllLogs()[0]
	resultLR := resultLogs.ResourceLogs().At(0).ScopeLogs().At(0).LogRecords().At(0)
	ip, found := resultLR.Attributes().Get("client.ip")
	assert.True(t, found)
	assert.Equal(t, "10.0.0.50", ip.Str())
}

func TestLogsProcessor_ExtractAll(t *testing.T) {
	cfg := &Config{
		TargetAttribute:    "client.ip",
		SearchBody:         true,
		AttributesToSearch: []string{},
		ExtractAll:         true,
		ExtractIPv6:        false,
	}

	sink := &consumertest.LogsSink{}
	processor := &logsProcessor{
		config:       cfg,
		logger:       processortest.NewNopSettings(testProcessorType).Logger,
		nextConsumer: sink,
	}

	logs := createTestLogsWithBody("Connection from 192.168.1.100 to 10.0.0.1")

	err := processor.ConsumeLogs(context.Background(), logs)
	require.NoError(t, err)

	// Verify both IPs were extracted to log record attributes
	require.Equal(t, 1, sink.LogRecordCount())
	resultLogs := sink.AllLogs()[0]
	lr := resultLogs.ResourceLogs().At(0).ScopeLogs().At(0).LogRecords().At(0)
	ip, found := lr.Attributes().Get("client.ip")
	assert.True(t, found)
	assert.Equal(t, "192.168.1.100,10.0.0.1", ip.Str())
}

func TestLogsProcessor_NoIP(t *testing.T) {
	cfg := &Config{
		TargetAttribute:    "client.ip",
		SearchBody:         true,
		AttributesToSearch: []string{"original_message"},
		ExtractAll:         false,
		ExtractIPv6:        false,
	}

	sink := &consumertest.LogsSink{}
	processor := &logsProcessor{
		config:       cfg,
		logger:       processortest.NewNopSettings(testProcessorType).Logger,
		nextConsumer: sink,
	}

	logs := createTestLogsWithBody("This log has no IP address")

	err := processor.ConsumeLogs(context.Background(), logs)
	require.NoError(t, err)

	// Verify no IP attribute was added to log record
	require.Equal(t, 1, sink.LogRecordCount())
	resultLogs := sink.AllLogs()[0]
	lr := resultLogs.ResourceLogs().At(0).ScopeLogs().At(0).LogRecords().At(0)
	_, found := lr.Attributes().Get("client.ip")
	assert.False(t, found)
}

func TestLogsProcessor_ExcludesLoopback(t *testing.T) {
	cfg := &Config{
		TargetAttribute:    "client.ip",
		SearchBody:         true,
		AttributesToSearch: []string{},
		ExtractAll:         false,
		ExtractIPv6:        false,
	}

	sink := &consumertest.LogsSink{}
	processor := &logsProcessor{
		config:       cfg,
		logger:       processortest.NewNopSettings(testProcessorType).Logger,
		nextConsumer: sink,
	}

	logs := createTestLogsWithBody("Listening on 127.0.0.1:8080")

	err := processor.ConsumeLogs(context.Background(), logs)
	require.NoError(t, err)

	// Loopback should be excluded
	resultLogs := sink.AllLogs()[0]
	lr := resultLogs.ResourceLogs().At(0).ScopeLogs().At(0).LogRecords().At(0)
	_, found := lr.Attributes().Get("client.ip")
	assert.False(t, found)
}

func TestLogsProcessor_SkipsLoopbackExtractsReal(t *testing.T) {
	cfg := &Config{
		TargetAttribute:    "client.ip",
		SearchBody:         true,
		AttributesToSearch: []string{},
		ExtractAll:         false,
		ExtractIPv6:        false,
	}

	sink := &consumertest.LogsSink{}
	processor := &logsProcessor{
		config:       cfg,
		logger:       processortest.NewNopSettings(testProcessorType).Logger,
		nextConsumer: sink,
	}

	logs := createTestLogsWithBody("Forwarding from 127.0.0.1 to 192.168.1.50")

	err := processor.ConsumeLogs(context.Background(), logs)
	require.NoError(t, err)

	// Should extract 192.168.1.50, not 127.0.0.1
	resultLogs := sink.AllLogs()[0]
	lr := resultLogs.ResourceLogs().At(0).ScopeLogs().At(0).LogRecords().At(0)
	ip, found := lr.Attributes().Get("client.ip")
	assert.True(t, found)
	assert.Equal(t, "192.168.1.50", ip.Str())
}

func TestLogsProcessor_OnlySearchesWhitelistedAttributes(t *testing.T) {
	cfg := &Config{
		TargetAttribute:    "client.ip",
		SearchBody:         false,
		AttributesToSearch: []string{"original_message"},
		ExtractAll:         false,
		ExtractIPv6:        false,
	}

	sink := &consumertest.LogsSink{}
	processor := &logsProcessor{
		config:       cfg,
		logger:       processortest.NewNopSettings(testProcessorType).Logger,
		nextConsumer: sink,
	}

	// Create logs with IP in original_message but not in other_attr
	logs := plog.NewLogs()
	rl := logs.ResourceLogs().AppendEmpty()
	sl := rl.ScopeLogs().AppendEmpty()
	lr := sl.LogRecords().AppendEmpty()
	lr.Attributes().PutStr("original_message", "From 192.168.1.100")
	lr.Attributes().PutStr("other_attr", "From 10.0.0.50")

	err := processor.ConsumeLogs(context.Background(), logs)
	require.NoError(t, err)

	// Only the IP from original_message should be extracted
	resultLogs := sink.AllLogs()[0]
	resultLR := resultLogs.ResourceLogs().At(0).ScopeLogs().At(0).LogRecords().At(0)
	ip, found := resultLR.Attributes().Get("client.ip")
	assert.True(t, found)
	assert.Equal(t, "192.168.1.100", ip.Str())
}

// Test: body has no IP, network metadata attrs have IPs but they're not whitelisted
// This is the bug fix test - no spurious client.ip should be set
func TestLogsProcessor_NoIPInBodyIgnoresNetworkAttrs(t *testing.T) {
	cfg := &Config{
		TargetAttribute:    "client.ip",
		SearchBody:         true,
		AttributesToSearch: []string{"original_message"}, // only search this attr
		ExtractAll:         false,
		ExtractIPv6:        false,
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
	lr.Body().SetStr("prefix length should be 64 for em0") // no IP in body
	lr.Attributes().PutStr("net.host.ip", "192.0.2.1")
	lr.Attributes().PutStr("net.peer.ip", "192.0.2.1")
	lr.Attributes().PutStr("source.ip", "192.0.2.1")
	// no original_message attribute - should not find any IP

	err := processor.ConsumeLogs(context.Background(), logs)
	require.NoError(t, err)

	// No IP should be extracted - body has none, net.* attrs are not whitelisted
	resultLogs := sink.AllLogs()[0]
	resultLR := resultLogs.ResourceLogs().At(0).ScopeLogs().At(0).LogRecords().At(0)
	_, found := resultLR.Attributes().Get("client.ip")
	assert.False(t, found, "client.ip should not be set when body has no IP and network attrs are not whitelisted")
}

func TestLogsProcessor_CustomTargetAttribute(t *testing.T) {
	cfg := &Config{
		TargetAttribute:    "source.ip.extracted",
		SearchBody:         true,
		AttributesToSearch: []string{},
		ExtractAll:         false,
		ExtractIPv6:        false,
	}

	sink := &consumertest.LogsSink{}
	processor := &logsProcessor{
		config:       cfg,
		logger:       processortest.NewNopSettings(testProcessorType).Logger,
		nextConsumer: sink,
	}

	logs := createTestLogsWithBody("Connection from 172.16.0.100")

	err := processor.ConsumeLogs(context.Background(), logs)
	require.NoError(t, err)

	// Verify IP was set to custom attribute on log record
	resultLogs := sink.AllLogs()[0]
	lr := resultLogs.ResourceLogs().At(0).ScopeLogs().At(0).LogRecords().At(0)
	ip, found := lr.Attributes().Get("source.ip.extracted")
	assert.True(t, found)
	assert.Equal(t, "172.16.0.100", ip.Str())
}

func TestLogsProcessor_SliceBody(t *testing.T) {
	cfg := &Config{
		TargetAttribute:    "client.ip",
		SearchBody:         true,
		AttributesToSearch: []string{},
		ExtractAll:         false,
		ExtractIPv6:        false,
	}

	sink := &consumertest.LogsSink{}
	processor := &logsProcessor{
		config:       cfg,
		logger:       processortest.NewNopSettings(testProcessorType).Logger,
		nextConsumer: sink,
	}

	// Create logs with slice body containing IP
	logs := plog.NewLogs()
	rl := logs.ResourceLogs().AppendEmpty()
	sl := rl.ScopeLogs().AppendEmpty()
	lr := sl.LogRecords().AppendEmpty()
	bodySlice := lr.Body().SetEmptySlice()
	bodySlice.AppendEmpty().SetStr("first line")
	bodySlice.AppendEmpty().SetStr("DHCPACK to 192.168.1.200")
	bodySlice.AppendEmpty().SetStr("third line")

	err := processor.ConsumeLogs(context.Background(), logs)
	require.NoError(t, err)

	// IP should be extracted from slice element
	resultLogs := sink.AllLogs()[0]
	resultLR := resultLogs.ResourceLogs().At(0).ScopeLogs().At(0).LogRecords().At(0)
	ip, found := resultLR.Attributes().Get("client.ip")
	assert.True(t, found)
	assert.Equal(t, "192.168.1.200", ip.Str())
}

func TestLogsProcessor_MapBody(t *testing.T) {
	cfg := &Config{
		TargetAttribute:    "client.ip",
		SearchBody:         true,
		AttributesToSearch: []string{},
		ExtractAll:         false,
		ExtractIPv6:        false,
	}

	sink := &consumertest.LogsSink{}
	processor := &logsProcessor{
		config:       cfg,
		logger:       processortest.NewNopSettings(testProcessorType).Logger,
		nextConsumer: sink,
	}

	// Create logs with map body containing IP (like journald logs)
	logs := plog.NewLogs()
	rl := logs.ResourceLogs().AppendEmpty()
	sl := rl.ScopeLogs().AppendEmpty()
	lr := sl.LogRecords().AppendEmpty()
	bodyMap := lr.Body().SetEmptyMap()
	bodyMap.PutStr("MESSAGE", "DHCPACK to 192.168.1.150")
	bodyMap.PutStr("_HOSTNAME", "server1")
	bodyMap.PutStr("_SYSTEMD_UNIT", "dhcpd.service")

	err := processor.ConsumeLogs(context.Background(), logs)
	require.NoError(t, err)

	// IP should be extracted from nested map
	resultLogs := sink.AllLogs()[0]
	resultLR := resultLogs.ResourceLogs().At(0).ScopeLogs().At(0).LogRecords().At(0)
	ip, found := resultLR.Attributes().Get("client.ip")
	assert.True(t, found)
	assert.Equal(t, "192.168.1.150", ip.Str())
}

// Test structured body with nested map - like journald with complex fields
func TestLogsProcessor_NestedMapBody(t *testing.T) {
	cfg := &Config{
		TargetAttribute:    "client.ip",
		SearchBody:         true,
		AttributesToSearch: []string{},
		ExtractAll:         false,
		ExtractIPv6:        false,
	}

	sink := &consumertest.LogsSink{}
	processor := &logsProcessor{
		config:       cfg,
		logger:       processortest.NewNopSettings(testProcessorType).Logger,
		nextConsumer: sink,
	}

	// Create logs with nested map body
	logs := plog.NewLogs()
	rl := logs.ResourceLogs().AppendEmpty()
	sl := rl.ScopeLogs().AppendEmpty()
	lr := sl.LogRecords().AppendEmpty()
	bodyMap := lr.Body().SetEmptyMap()
	bodyMap.PutStr("_HOSTNAME", "labhost-ds4")
	bodyMap.PutStr("PRIORITY", "6")
	bodyMap.PutStr("MESSAGE", "Client 192.0.2.1 connected")
	// Add nested map
	nestedMap := bodyMap.PutEmptyMap("_EXTRA")
	nestedMap.PutStr("nested_ip", "10.0.0.99")

	err := processor.ConsumeLogs(context.Background(), logs)
	require.NoError(t, err)

	// Should extract IP from MESSAGE field (first found)
	resultLogs := sink.AllLogs()[0]
	resultLR := resultLogs.ResourceLogs().At(0).ScopeLogs().At(0).LogRecords().At(0)
	ip, found := resultLR.Attributes().Get("client.ip")
	assert.True(t, found)
	// Order of map iteration is not guaranteed, but we should get one of the IPs
	assert.Contains(t, []string{"192.0.2.1", "10.0.0.99"}, ip.Str())
}

// Test: body is a map but has no IPs anywhere
func TestLogsProcessor_MapBodyNoIP(t *testing.T) {
	cfg := &Config{
		TargetAttribute:    "client.ip",
		SearchBody:         true,
		AttributesToSearch: []string{},
		ExtractAll:         false,
		ExtractIPv6:        false,
	}

	sink := &consumertest.LogsSink{}
	processor := &logsProcessor{
		config:       cfg,
		logger:       processortest.NewNopSettings(testProcessorType).Logger,
		nextConsumer: sink,
	}

	// Create journald-style structured log without IPs
	logs := plog.NewLogs()
	rl := logs.ResourceLogs().AppendEmpty()
	sl := rl.ScopeLogs().AppendEmpty()
	lr := sl.LogRecords().AppendEmpty()
	bodyMap := lr.Body().SetEmptyMap()
	bodyMap.PutStr("_HOSTNAME", "labhost-ds4")
	bodyMap.PutStr("PRIORITY", "6")
	bodyMap.PutStr("MESSAGE", "Started network service")
	bodyMap.PutStr("_SYSTEMD_UNIT", "NetworkManager.service")

	err := processor.ConsumeLogs(context.Background(), logs)
	require.NoError(t, err)

	// No IP should be extracted
	resultLogs := sink.AllLogs()[0]
	resultLR := resultLogs.ResourceLogs().At(0).ScopeLogs().At(0).LogRecords().At(0)
	_, found := resultLR.Attributes().Get("client.ip")
	assert.False(t, found, "client.ip should not be set when no IPs in body")
}

// Test: Missing whitelisted attribute should not crash
func TestLogsProcessor_MissingAttribute(t *testing.T) {
	cfg := &Config{
		TargetAttribute:    "client.ip",
		SearchBody:         false,
		AttributesToSearch: []string{"original_message", "nonexistent_attr"},
		ExtractAll:         false,
		ExtractIPv6:        false,
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
	lr.Body().SetStr("some body without IP")
	lr.Attributes().PutStr("other_attr", "From 192.168.1.1") // not in whitelist

	err := processor.ConsumeLogs(context.Background(), logs)
	require.NoError(t, err)

	// Should not crash, no IP extracted
	resultLogs := sink.AllLogs()[0]
	resultLR := resultLogs.ResourceLogs().At(0).ScopeLogs().At(0).LogRecords().At(0)
	_, found := resultLR.Attributes().Get("client.ip")
	assert.False(t, found, "client.ip should not be set when whitelisted attr is missing")
}

// Test: Attribute exists but is not a string (e.g., int, map)
func TestLogsProcessor_NonStringAttribute(t *testing.T) {
	cfg := &Config{
		TargetAttribute:    "client.ip",
		SearchBody:         false,
		AttributesToSearch: []string{"original_message"},
		ExtractAll:         false,
		ExtractIPv6:        false,
	}

	sink := &consumertest.LogsSink{}
	processor := &logsProcessor{
		config:       cfg,
		logger:       processortest.NewNopSettings(testProcessorType).Logger,
		nextConsumer: sink,
	}

	// Create logs where original_message is a map (not a string)
	logs := plog.NewLogs()
	rl := logs.ResourceLogs().AppendEmpty()
	sl := rl.ScopeLogs().AppendEmpty()
	lr := sl.LogRecords().AppendEmpty()
	// Set original_message as a map instead of string
	attrMap := lr.Attributes().PutEmptyMap("original_message")
	attrMap.PutStr("text", "IP is 192.168.1.100")
	attrMap.PutStr("format", "raw")

	err := processor.ConsumeLogs(context.Background(), logs)
	require.NoError(t, err)

	// Should handle map attribute and extract IP from nested string
	resultLogs := sink.AllLogs()[0]
	resultLR := resultLogs.ResourceLogs().At(0).ScopeLogs().At(0).LogRecords().At(0)
	ip, found := resultLR.Attributes().Get("client.ip")
	assert.True(t, found)
	assert.Equal(t, "192.168.1.100", ip.Str())
}

func TestLogsProcessor_MultipleLogRecords(t *testing.T) {
	cfg := &Config{
		TargetAttribute:    "client.ip",
		SearchBody:         true,
		AttributesToSearch: []string{},
		ExtractAll:         false,
		ExtractIPv6:        false,
	}

	sink := &consumertest.LogsSink{}
	processor := &logsProcessor{
		config:       cfg,
		logger:       processortest.NewNopSettings(testProcessorType).Logger,
		nextConsumer: sink,
	}

	// Create logs with multiple records - some with IP, some without
	logs := plog.NewLogs()
	rl := logs.ResourceLogs().AppendEmpty()
	sl := rl.ScopeLogs().AppendEmpty()

	lr1 := sl.LogRecords().AppendEmpty()
	lr1.Body().SetStr("DHCPACK to 192.168.1.100")

	lr2 := sl.LogRecords().AppendEmpty()
	lr2.Body().SetStr("No IP here")

	lr3 := sl.LogRecords().AppendEmpty()
	lr3.Body().SetStr("DHCPREQUEST from 192.168.1.200")

	err := processor.ConsumeLogs(context.Background(), logs)
	require.NoError(t, err)

	// Each log record should be processed independently
	require.Equal(t, 3, sink.LogRecordCount())
	resultLogs := sink.AllLogs()[0]
	logRecords := resultLogs.ResourceLogs().At(0).ScopeLogs().At(0).LogRecords()

	// lr1 has IP
	ip1, found1 := logRecords.At(0).Attributes().Get("client.ip")
	assert.True(t, found1, "lr1 should have IP")
	assert.Equal(t, "192.168.1.100", ip1.Str())

	// lr2 has NO IP
	_, found2 := logRecords.At(1).Attributes().Get("client.ip")
	assert.False(t, found2, "lr2 should NOT have IP")

	// lr3 has IP
	ip3, found3 := logRecords.At(2).Attributes().Get("client.ip")
	assert.True(t, found3, "lr3 should have IP")
	assert.Equal(t, "192.168.1.200", ip3.Str())
}

func TestFactory_CreateDefaultConfig(t *testing.T) {
	factory := NewFactory()
	cfg := factory.CreateDefaultConfig()

	assert.NotNil(t, cfg)
	processorCfg, ok := cfg.(*Config)
	assert.True(t, ok)
	assert.Equal(t, "client.ip", processorCfg.TargetAttribute)
	assert.True(t, processorCfg.SearchBody)
	assert.Equal(t, []string{"original_message"}, processorCfg.AttributesToSearch)
	assert.False(t, processorCfg.ExtractAll)
	assert.False(t, processorCfg.ExtractIPv6)
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

// IP extraction tests

func TestExtractIPs_IPv4(t *testing.T) {
	testCases := []struct {
		name  string
		input string
		want  []string
	}{
		{"single_ip", "Connection from 192.168.1.100", []string{"192.168.1.100"}},
		{"ip_with_port", "Server at 10.0.0.1:8080", []string{"10.0.0.1"}},
		{"multiple_ips", "From 192.168.1.1 to 10.0.0.1", []string{"192.168.1.1", "10.0.0.1"}},
		{"dhcp_message", "DHCPACK on 192.0.2.1 to aa:bb:cc:dd:ee:ff", []string{"192.0.2.1"}},
		{"no_ip", "No IP address here", nil},
		{"empty", "", nil},
		{"loopback_excluded", "localhost 127.0.0.1", nil},
		{"zero_excluded", "0.0.0.0 binding", nil},
		{"private_class_a", "Host 10.255.255.255", []string{"10.255.255.255"}},
		{"private_class_b", "Host 172.16.0.1", []string{"172.16.0.1"}},
		{"private_class_c", "Host 192.168.0.1", []string{"192.168.0.1"}},
		{"public_ip", "Server 8.8.8.8", []string{"8.8.8.8"}},
		{"max_octets", "IP 255.255.255.255", []string{"255.255.255.255"}},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got := ExtractIPs(tc.input, false)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestExtractFirstIP(t *testing.T) {
	testCases := []struct {
		input string
		want  string
	}{
		{"Connection from 192.168.1.100 to 10.0.0.1", "192.168.1.100"},
		{"No IP here", ""},
		{"", ""},
	}

	for _, tc := range testCases {
		t.Run(tc.input, func(t *testing.T) {
			got := ExtractFirstIP(tc.input, false)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestIsValidIPv4(t *testing.T) {
	assert.True(t, IsValidIPv4("192.168.1.1"))
	assert.True(t, IsValidIPv4("10.0.0.1"))
	assert.True(t, IsValidIPv4("255.255.255.255"))
	assert.False(t, IsValidIPv4("invalid"))
	assert.False(t, IsValidIPv4(""))
	assert.False(t, IsValidIPv4("::1"))
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
