// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package ciscologprocessor

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

var testProcessorType = component.MustNewType("ciscolog")

func TestFactory_CreateDefaultConfig(t *testing.T) {
	factory := NewFactory()
	cfg := factory.CreateDefaultConfig()

	assert.NotNil(t, cfg)
	processorCfg, ok := cfg.(*Config)
	assert.True(t, ok)
	assert.Equal(t, "original_message", processorCfg.SourceAttribute)
	assert.True(t, processorCfg.SetHostname)
	assert.True(t, processorCfg.CleanBody)
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

func TestLogsProcessor_CiscoMnemonic(t *testing.T) {
	cfg := &Config{
		SourceAttribute: "original_message",
		SetHostname:     true,
		CleanBody:       true,
	}

	sink := &consumertest.LogsSink{}
	processor := &logsProcessor{
		config:       cfg,
		logger:       processortest.NewNopSettings(testProcessorType).Logger,
		nextConsumer: sink,
	}

	// Create log with Cisco mnemonic in original_message (WLC format - not parsed by OTTL)
	logs := plog.NewLogs()
	rl := logs.ResourceLogs().AppendEmpty()
	sl := rl.ScopeLogs().AppendEmpty()
	lr := sl.LogRecords().AppendEmpty()
	lr.Attributes().PutStr("original_message", "DownAP-Master: *apfMsConnTask_0: Dec 15 20:49:14.582: %APF-5-CLIENT_ASSOCIATE: Client Association")
	lr.Attributes().PutStr("net.peer.name", "wifi-ap.home.example.com")
	lr.Body().SetStr("<133>DownAP-Master: message")

	err := processor.ConsumeLogs(context.Background(), logs)
	require.NoError(t, err)

	// Verify Cisco attributes were extracted
	require.Equal(t, 1, sink.LogRecordCount())
	resultLogs := sink.AllLogs()[0]
	resultRL := resultLogs.ResourceLogs().At(0)
	resultLR := resultRL.ScopeLogs().At(0).LogRecords().At(0)

	// Check log record attributes
	facility, found := resultLR.Attributes().Get("cisco_facility")
	assert.True(t, found)
	assert.Equal(t, "APF", facility.Str())

	mnemonic, found := resultLR.Attributes().Get("cisco_mnemonic")
	assert.True(t, found)
	assert.Equal(t, "CLIENT_ASSOCIATE", mnemonic.Str())

	severity, found := resultLR.Attributes().Get("cisco_severity")
	assert.True(t, found)
	assert.Equal(t, "5", severity.Str())

	// Check resource attributes
	hostName, found := resultRL.Resource().Attributes().Get("host.name")
	assert.True(t, found)
	assert.Equal(t, "wifi-ap.home.example.com", hostName.Str())

	serviceName, found := resultRL.Resource().Attributes().Get("service.name")
	assert.True(t, found)
	assert.Equal(t, "APF", serviceName.Str())

	// Check body was cleaned
	assert.Equal(t, "DownAP-Master: message", resultLR.Body().Str())
}

func TestLogsProcessor_SkipsAlreadyParsed(t *testing.T) {
	cfg := &Config{
		SourceAttribute: "original_message",
		SetHostname:     true,
		CleanBody:       true,
	}

	sink := &consumertest.LogsSink{}
	processor := &logsProcessor{
		config:       cfg,
		logger:       processortest.NewNopSettings(testProcessorType).Logger,
		nextConsumer: sink,
	}

	// Create log that was already parsed by transform/syslog
	logs := plog.NewLogs()
	rl := logs.ResourceLogs().AppendEmpty()
	rl.Resource().Attributes().PutStr("host.name", "switch1")
	rl.Resource().Attributes().PutStr("service.name", "SW_MATM")
	sl := rl.ScopeLogs().AppendEmpty()
	lr := sl.LogRecords().AppendEmpty()
	lr.Attributes().PutStr("cisco_facility", "SW_MATM")
	lr.Attributes().PutStr("cisco_mnemonic", "MACFLAP_NOTIF")
	lr.Body().SetStr("<189>body content")

	err := processor.ConsumeLogs(context.Background(), logs)
	require.NoError(t, err)

	// Should still clean body
	resultLogs := sink.AllLogs()[0]
	resultLR := resultLogs.ResourceLogs().At(0).ScopeLogs().At(0).LogRecords().At(0)
	assert.Equal(t, "body content", resultLR.Body().Str())

	// Should not modify existing attributes
	facility, _ := resultLR.Attributes().Get("cisco_facility")
	assert.Equal(t, "SW_MATM", facility.Str())
}

func TestLogsProcessor_HPLog(t *testing.T) {
	cfg := &Config{
		SourceAttribute: "original_message",
		SetHostname:     true,
		CleanBody:       true,
	}

	sink := &consumertest.LogsSink{}
	processor := &logsProcessor{
		config:       cfg,
		logger:       processortest.NewNopSettings(testProcessorType).Logger,
		nextConsumer: sink,
	}

	// Create log with HP format
	logs := plog.NewLogs()
	rl := logs.ResourceLogs().AppendEmpty()
	sl := rl.ScopeLogs().AppendEmpty()
	lr := sl.LogRecords().AppendEmpty()
	lr.Attributes().PutStr("original_message", " Dec 15 21:31:30 switch-c-1 General[tRpcsrv.00001]: usmdb_sim.c(3847) 2805 %% Event(0x0)")
	lr.Body().SetStr("<10> Dec 15 21:31:30 switch-c-1 General[tRpcsrv.00001]: message")

	err := processor.ConsumeLogs(context.Background(), logs)
	require.NoError(t, err)

	// Verify HP attributes were extracted
	resultLogs := sink.AllLogs()[0]
	resultRL := resultLogs.ResourceLogs().At(0)
	resultLR := resultRL.ScopeLogs().At(0).LogRecords().At(0)

	// Check resource attributes
	hostName, found := resultRL.Resource().Attributes().Get("host.name")
	assert.True(t, found)
	assert.Equal(t, "switch-c-1", hostName.Str())

	serviceName, found := resultRL.Resource().Attributes().Get("service.name")
	assert.True(t, found)
	assert.Equal(t, "General", serviceName.Str())

	// Check process name
	processName, found := resultLR.Attributes().Get("process.name")
	assert.True(t, found)
	assert.Equal(t, "tRpcsrv.00001", processName.Str())

	// Check body was cleaned
	assert.Equal(t, "Dec 15 21:31:30 switch-c-1 General[tRpcsrv.00001]: message", resultLR.Body().Str())
}

func TestLogsProcessor_BareMnemonic(t *testing.T) {
	cfg := &Config{
		SourceAttribute: "original_message",
		SetHostname:     true,
		CleanBody:       true,
	}

	sink := &consumertest.LogsSink{}
	processor := &logsProcessor{
		config:       cfg,
		logger:       processortest.NewNopSettings(testProcessorType).Logger,
		nextConsumer: sink,
	}

	// Create log with bare Cisco mnemonic (letter severity)
	logs := plog.NewLogs()
	rl := logs.ResourceLogs().AppendEmpty()
	sl := rl.ScopeLogs().AppendEmpty()
	lr := sl.LogRecords().AppendEmpty()
	lr.Attributes().PutStr("original_message", "%COPY-N-TRAP: The copy operation was completed successfully")
	lr.Attributes().PutStr("net.peer.name", "switch-a.home.example.com")
	lr.Body().SetStr("<189>%COPY-N-TRAP: The copy operation was completed successfully")

	err := processor.ConsumeLogs(context.Background(), logs)
	require.NoError(t, err)

	// Verify Cisco attributes were extracted
	resultLogs := sink.AllLogs()[0]
	resultRL := resultLogs.ResourceLogs().At(0)
	resultLR := resultRL.ScopeLogs().At(0).LogRecords().At(0)

	facility, found := resultLR.Attributes().Get("cisco_facility")
	assert.True(t, found)
	assert.Equal(t, "COPY", facility.Str())

	mnemonic, found := resultLR.Attributes().Get("cisco_mnemonic")
	assert.True(t, found)
	assert.Equal(t, "TRAP", mnemonic.Str())

	severity, found := resultLR.Attributes().Get("cisco_severity")
	assert.True(t, found)
	assert.Equal(t, "N", severity.Str())

	// Check hostname was set
	hostName, found := resultRL.Resource().Attributes().Get("host.name")
	assert.True(t, found)
	assert.Equal(t, "switch-a.home.example.com", hostName.Str())

	// Check body was cleaned
	assert.Equal(t, "%COPY-N-TRAP: The copy operation was completed successfully", resultLR.Body().Str())
}

func TestLogsProcessor_NoMatch(t *testing.T) {
	cfg := &Config{
		SourceAttribute: "original_message",
		SetHostname:     true,
		CleanBody:       true,
	}

	sink := &consumertest.LogsSink{}
	processor := &logsProcessor{
		config:       cfg,
		logger:       processortest.NewNopSettings(testProcessorType).Logger,
		nextConsumer: sink,
	}

	// Create log with no Cisco pattern
	logs := plog.NewLogs()
	rl := logs.ResourceLogs().AppendEmpty()
	sl := rl.ScopeLogs().AppendEmpty()
	lr := sl.LogRecords().AppendEmpty()
	lr.Attributes().PutStr("original_message", "Oct 15 09:23:45 server sshd[1234]: Accepted password")
	lr.Attributes().PutStr("net.peer.name", "server.example.com")
	lr.Body().SetStr("<134>Oct 15 09:23:45 server sshd[1234]: Accepted password")

	err := processor.ConsumeLogs(context.Background(), logs)
	require.NoError(t, err)

	// Should not add cisco attributes
	resultLogs := sink.AllLogs()[0]
	resultRL := resultLogs.ResourceLogs().At(0)
	resultLR := resultRL.ScopeLogs().At(0).LogRecords().At(0)

	_, found := resultLR.Attributes().Get("cisco_facility")
	assert.False(t, found)

	// Should still set hostname from net.peer.name
	hostName, found := resultRL.Resource().Attributes().Get("host.name")
	assert.True(t, found)
	assert.Equal(t, "server.example.com", hostName.Str())

	// Should still clean body
	assert.Equal(t, "Oct 15 09:23:45 server sshd[1234]: Accepted password", resultLR.Body().Str())
}

func TestLogsProcessor_FallbackToBody(t *testing.T) {
	cfg := &Config{
		SourceAttribute: "original_message",
		SetHostname:     true,
		CleanBody:       true,
	}

	sink := &consumertest.LogsSink{}
	processor := &logsProcessor{
		config:       cfg,
		logger:       processortest.NewNopSettings(testProcessorType).Logger,
		nextConsumer: sink,
	}

	// Create log with Cisco mnemonic only in body (no original_message attr)
	logs := plog.NewLogs()
	rl := logs.ResourceLogs().AppendEmpty()
	sl := rl.ScopeLogs().AppendEmpty()
	lr := sl.LogRecords().AppendEmpty()
	lr.Body().SetStr("%LINK-3-UPDOWN: Interface GigabitEthernet0/1, changed state to up")

	err := processor.ConsumeLogs(context.Background(), logs)
	require.NoError(t, err)

	// Should extract from body as fallback
	resultLogs := sink.AllLogs()[0]
	resultLR := resultLogs.ResourceLogs().At(0).ScopeLogs().At(0).LogRecords().At(0)

	facility, found := resultLR.Attributes().Get("cisco_facility")
	assert.True(t, found)
	assert.Equal(t, "LINK", facility.Str())
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

func TestLogsProcessor_HPLogWithCodeLocation(t *testing.T) {
	cfg := &Config{
		SourceAttribute: "original_message",
		SetHostname:     true,
		CleanBody:       true,
		RewriteMessages: true,
		AdjustSeverity:  true,
	}

	sink := &consumertest.LogsSink{}
	processor := &logsProcessor{
		config:       cfg,
		logger:       processortest.NewNopSettings(testProcessorType).Logger,
		nextConsumer: sink,
	}

	// Create log with HP format including code location
	logs := plog.NewLogs()
	rl := logs.ResourceLogs().AppendEmpty()
	sl := rl.ScopeLogs().AppendEmpty()
	lr := sl.LogRecords().AppendEmpty()
	lr.Attributes().PutStr("original_message", " Dec 15 22:56:14 switch-c-1 TRAPMGR[dot1s_task]: traputil.c(763) 284 %% Spanning Tree Topology Change Received")
	lr.Body().SetStr("<10> Dec 15 22:56:14 switch-c-1 TRAPMGR[dot1s_task]: message")

	err := processor.ConsumeLogs(context.Background(), logs)
	require.NoError(t, err)

	resultLogs := sink.AllLogs()[0]
	resultRL := resultLogs.ResourceLogs().At(0)
	resultLR := resultRL.ScopeLogs().At(0).LogRecords().At(0)

	// Check code location attributes
	codeFilepath, found := resultLR.Attributes().Get("code.filepath")
	assert.True(t, found)
	assert.Equal(t, "traputil.c", codeFilepath.Str())

	codeLineno, found := resultLR.Attributes().Get("code.lineno")
	assert.True(t, found)
	assert.Equal(t, "763", codeLineno.Str())

	logSequence, found := resultLR.Attributes().Get("log.sequence")
	assert.True(t, found)
	assert.Equal(t, "284", logSequence.Str())

	processName, found := resultLR.Attributes().Get("process.name")
	assert.True(t, found)
	assert.Equal(t, "dot1s_task", processName.Str())

	// Check body was rewritten to clean message
	assert.Equal(t, "Spanning Tree Topology Change Received", resultLR.Body().Str())
}

func TestLogsProcessor_SeverityAdjustment(t *testing.T) {
	cfg := &Config{
		SourceAttribute: "original_message",
		SetHostname:     true,
		CleanBody:       true,
		RewriteMessages: true,
		AdjustSeverity:  true,
	}

	sink := &consumertest.LogsSink{}
	processor := &logsProcessor{
		config:       cfg,
		logger:       processortest.NewNopSettings(testProcessorType).Logger,
		nextConsumer: sink,
	}

	// Create log with UPDOWN mnemonic (normally severity 3/ERROR, should be adjusted to WARN)
	logs := plog.NewLogs()
	rl := logs.ResourceLogs().AppendEmpty()
	sl := rl.ScopeLogs().AppendEmpty()
	lr := sl.LogRecords().AppendEmpty()
	lr.Attributes().PutStr("original_message", "%LINK-3-UPDOWN: Interface GigabitEthernet0/1, changed state to down")
	lr.Body().SetStr("%LINK-3-UPDOWN: Interface GigabitEthernet0/1, changed state to down")

	err := processor.ConsumeLogs(context.Background(), logs)
	require.NoError(t, err)

	resultLogs := sink.AllLogs()[0]
	resultLR := resultLogs.ResourceLogs().At(0).ScopeLogs().At(0).LogRecords().At(0)

	// Verify severity was adjusted to WARN (not ERROR)
	assert.Equal(t, plog.SeverityNumberWarn, resultLR.SeverityNumber())
	assert.Equal(t, "WARN", resultLR.SeverityText())

	// Verify interface was extracted
	ifName, found := resultLR.Attributes().Get("network.interface.name")
	assert.True(t, found)
	assert.Equal(t, "GigabitEthernet0/1", ifName.Str())
}

func TestLogsProcessor_InterfaceExtraction(t *testing.T) {
	cfg := &Config{
		SourceAttribute: "original_message",
		SetHostname:     true,
		CleanBody:       true,
		RewriteMessages: true,
		AdjustSeverity:  true,
	}

	sink := &consumertest.LogsSink{}
	processor := &logsProcessor{
		config:       cfg,
		logger:       processortest.NewNopSettings(testProcessorType).Logger,
		nextConsumer: sink,
	}

	// Create log with MAC flap mentioning interfaces
	logs := plog.NewLogs()
	rl := logs.ResourceLogs().AppendEmpty()
	sl := rl.ScopeLogs().AppendEmpty()
	lr := sl.LogRecords().AppendEmpty()
	lr.Attributes().PutStr("original_message", "661: *Dec 16 00:36:36.024: %SW_MATM-4-MACFLAP_NOTIF: Host 6212.24a7.dd56 in vlan 1 is flapping between port Gi4/0/45 and port Gi4/0/43")
	lr.Body().SetStr("Host flapping between ports")

	err := processor.ConsumeLogs(context.Background(), logs)
	require.NoError(t, err)

	resultLogs := sink.AllLogs()[0]
	resultLR := resultLogs.ResourceLogs().At(0).ScopeLogs().At(0).LogRecords().At(0)

	// Verify first interface was extracted
	ifName, found := resultLR.Attributes().Get("network.interface.name")
	assert.True(t, found)
	assert.Equal(t, "Gi4/0/45", ifName.Str())

	// Verify severity was adjusted to WARN for MACFLAP_NOTIF
	assert.Equal(t, plog.SeverityNumberWarn, resultLR.SeverityNumber())
	assert.Equal(t, "WARN", resultLR.SeverityText())
}

func TestLogsProcessor_DefaultConfigValues(t *testing.T) {
	factory := NewFactory()
	cfg := factory.CreateDefaultConfig()

	processorCfg, ok := cfg.(*Config)
	assert.True(t, ok)
	assert.True(t, processorCfg.RewriteMessages)
	assert.True(t, processorCfg.AdjustSeverity)
}

func TestLogsProcessor_WLCLog(t *testing.T) {
	cfg := &Config{
		SourceAttribute: "original_message",
		SetHostname:     true,
		CleanBody:       true,
		RewriteMessages: true,
		AdjustSeverity:  true,
	}

	sink := &consumertest.LogsSink{}
	processor := &logsProcessor{
		config:       cfg,
		logger:       processortest.NewNopSettings(testProcessorType).Logger,
		nextConsumer: sink,
	}

	// Create log with WLC format
	logs := plog.NewLogs()
	rl := logs.ResourceLogs().AppendEmpty()
	sl := rl.ScopeLogs().AppendEmpty()
	lr := sl.LogRecords().AppendEmpty()
	lr.Attributes().PutStr("original_message", "DownAP-Master: *apfMsConnTask_0: Dec 16 00:00:44.695: %APF-5-CLIENT_ASSOCIATE: apf_80211.c:13338 Client Association: Client MAC: c4:4f:33:91:a6:58, AP Name: CiscoAP-Down, Radio: 2.4GHz , WLAN Id: 1.")
	lr.Attributes().PutStr("net.peer.name", "wifi-ap.home.example.com")
	lr.Body().SetStr("DownAP-Master: *apfMsConnTask_0: Dec 16 00:00:44.695: %APF-5-CLIENT_ASSOCIATE: apf_80211.c:13338 Client Association: Client MAC: c4:4f:33:91:a6:58, AP Name: CiscoAP-Down, Radio: 2.4GHz , WLAN Id: 1.")

	err := processor.ConsumeLogs(context.Background(), logs)
	require.NoError(t, err)

	resultLogs := sink.AllLogs()[0]
	resultRL := resultLogs.ResourceLogs().At(0)
	resultLR := resultRL.ScopeLogs().At(0).LogRecords().At(0)

	// Verify Cisco attributes were extracted
	facility, found := resultLR.Attributes().Get("cisco_facility")
	assert.True(t, found)
	assert.Equal(t, "APF", facility.Str())

	mnemonic, found := resultLR.Attributes().Get("cisco_mnemonic")
	assert.True(t, found)
	assert.Equal(t, "CLIENT_ASSOCIATE", mnemonic.Str())

	// Verify WLC-specific attributes
	apName, found := resultLR.Attributes().Get("wifi.ap.name")
	assert.True(t, found, "wifi.ap.name should be set")
	assert.Equal(t, "DownAP-Master", apName.Str())

	threadName, found := resultLR.Attributes().Get("thread.name")
	assert.True(t, found, "thread.name should be set")
	assert.Equal(t, "apfMsConnTask_0", threadName.Str())

	codeFilepath, found := resultLR.Attributes().Get("code.filepath")
	assert.True(t, found, "code.filepath should be set")
	assert.Equal(t, "apf_80211.c", codeFilepath.Str())

	codeLineno, found := resultLR.Attributes().Get("code.lineno")
	assert.True(t, found, "code.lineno should be set")
	assert.Equal(t, "13338", codeLineno.Str())

	clientAP, found := resultLR.Attributes().Get("wifi.client.ap")
	assert.True(t, found, "wifi.client.ap should be set")
	assert.Equal(t, "CiscoAP-Down", clientAP.Str())

	radio, found := resultLR.Attributes().Get("wifi.radio")
	assert.True(t, found, "wifi.radio should be set")
	assert.Equal(t, "2.4GHz", radio.Str())

	wlanId, found := resultLR.Attributes().Get("wifi.wlan.id")
	assert.True(t, found, "wifi.wlan.id should be set")
	assert.Equal(t, "1", wlanId.Str())

	// Verify host.name was set
	hostName, found := resultRL.Resource().Attributes().Get("host.name")
	assert.True(t, found)
	assert.Equal(t, "wifi-ap.home.example.com", hostName.Str())

	// Verify body was rewritten to clean message
	assert.Contains(t, resultLR.Body().Str(), "Client Association")
	assert.NotContains(t, resultLR.Body().Str(), "DownAP-Master:")
}
