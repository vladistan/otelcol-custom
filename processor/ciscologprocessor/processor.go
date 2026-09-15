// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package ciscologprocessor

import (
	"context"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.uber.org/zap"
)

type logsProcessor struct {
	config       *Config
	logger       *zap.Logger
	nextConsumer consumer.Logs
	portDB       *portDB
}

func (p *logsProcessor) Start(_ context.Context, _ component.Host) error {
	// Load port database if configured
	if p.config.PortDatabasePath != "" {
		db, err := loadPortDB(p.config.PortDatabasePath)
		if err != nil {
			return err
		}
		p.portDB = db
		p.logger.Info("Loaded port database",
			zap.String("path", p.config.PortDatabasePath),
			zap.Int("entries", db.Size()),
		)
	}

	p.logger.Info("Cisco log processor started",
		zap.String("source_attribute", p.config.SourceAttribute),
		zap.Bool("set_hostname", p.config.SetHostname),
		zap.Bool("clean_body", p.config.CleanBody),
		zap.Bool("rewrite_messages", p.config.RewriteMessages),
		zap.Bool("adjust_severity", p.config.AdjustSeverity),
	)
	return nil
}

func (p *logsProcessor) Shutdown(_ context.Context) error {
	return nil
}

func (p *logsProcessor) Capabilities() consumer.Capabilities {
	return consumer.Capabilities{MutatesData: true}
}

func (p *logsProcessor) ConsumeLogs(ctx context.Context, ld plog.Logs) error {
	for i := 0; i < ld.ResourceLogs().Len(); i++ {
		rl := ld.ResourceLogs().At(i)
		resourceAttrs := rl.Resource().Attributes()

		for j := 0; j < rl.ScopeLogs().Len(); j++ {
			sl := rl.ScopeLogs().At(j)
			for k := 0; k < sl.LogRecords().Len(); k++ {
				lr := sl.LogRecords().At(k)
				p.processLogRecord(lr, resourceAttrs)
			}
		}
	}
	return p.nextConsumer.ConsumeLogs(ctx, ld)
}

func (p *logsProcessor) processLogRecord(lr plog.LogRecord, resourceAttrs pcommon.Map) {
	attrs := lr.Attributes()

	// Get the source text to search
	var sourceText string
	if val, ok := attrs.Get(p.config.SourceAttribute); ok {
		sourceText = val.Str()
	}

	// If source attribute not found, try the body
	if sourceText == "" {
		if lr.Body().Type() == pcommon.ValueTypeStr {
			sourceText = lr.Body().Str()
		}
	}

	// Get hostname for port lookups
	hostname := ""
	if val, ok := resourceAttrs.Get("host.name"); ok {
		hostname = val.Str()
	}

	// Skip if cisco_facility is already set (transform/syslog already parsed it)
	if _, found := attrs.Get("cisco_facility"); found {
		p.maybeSetHostname(resourceAttrs, attrs)
		p.maybeExtractInterface(lr, attrs, resourceAttrs, sourceText, hostname)
		p.maybeAdjustCiscoSeverity(lr, attrs)
		p.maybeCleanBody(lr)
		return
	}

	if sourceText == "" {
		return
	}

	// Try to parse as Cisco mnemonic
	if info := ParseCiscoMnemonic(sourceText); info != nil {
		attrs.PutStr("cisco_facility", info.Facility)
		attrs.PutStr("cisco_mnemonic", info.Mnemonic)
		attrs.PutStr("cisco_severity", info.Severity)

		// Set service.name to facility if not already set
		if _, found := resourceAttrs.Get("service.name"); !found {
			resourceAttrs.PutStr("service.name", info.Facility)
		}

		// Check if this is a WLC log (has AP: *thread: timestamp: prefix)
		if wlcInfo := ParseWLCLog(sourceText); wlcInfo != nil {
			p.processWLCLog(lr, attrs, resourceAttrs, info, wlcInfo)
			return
		}

		p.maybeSetHostname(resourceAttrs, attrs)

		// Update hostname for port lookups after potentially setting it
		if hostname == "" {
			if val, ok := resourceAttrs.Get("host.name"); ok {
				hostname = val.Str()
			}
		}

		p.maybeExtractInterface(lr, attrs, resourceAttrs, sourceText, hostname)
		p.maybeAdjustCiscoSeverity(lr, attrs)
		p.maybeCleanBody(lr)
		return
	}

	// Try to parse as HP log
	if info := ParseHPLog(sourceText); info != nil {
		p.processHPLog(lr, attrs, resourceAttrs, info)
		return
	}

	// No match - still try to set hostname and clean body
	p.maybeSetHostname(resourceAttrs, attrs)
	p.maybeCleanBody(lr)
}

// processHPLog handles HP switch log records
func (p *logsProcessor) processHPLog(lr plog.LogRecord, attrs pcommon.Map, resourceAttrs pcommon.Map, info *HPLogInfo) {
	// Set HP-specific attributes
	if _, found := resourceAttrs.Get("host.name"); !found {
		resourceAttrs.PutStr("host.name", info.Hostname)
	}
	if _, found := resourceAttrs.Get("service.name"); !found {
		resourceAttrs.PutStr("service.name", info.AppName)
	}
	if info.ProcessID != "" {
		attrs.PutStr("process.name", info.ProcessID)
	}

	// Set code location attributes (OTEL semantic conventions)
	if info.CodeFilepath != "" {
		attrs.PutStr("code.filepath", info.CodeFilepath)
	}
	if info.CodeLineno != "" {
		attrs.PutStr("code.lineno", info.CodeLineno)
	}
	if info.LogSequence != "" {
		attrs.PutStr("log.sequence", info.LogSequence)
	}

	// Rewrite body to clean message if configured
	if p.config.RewriteMessages && info.CleanMessage != "" {
		lr.Body().SetStr(info.CleanMessage)
	} else {
		p.maybeCleanBody(lr)
	}
}

// processWLCLog handles Cisco WLC (Wireless LAN Controller) log records
func (p *logsProcessor) processWLCLog(lr plog.LogRecord, attrs pcommon.Map, resourceAttrs pcommon.Map, ciscoInfo *CiscoLogInfo, wlcInfo *WLCLogInfo) {
	// Set hostname from net.peer.name if not already set
	p.maybeSetHostname(resourceAttrs, attrs)

	// Set WLC-specific attributes
	// wifi.ap.name - The AP that sent the log (e.g., DownAP-Master)
	if wlcInfo.APName != "" {
		attrs.PutStr("wifi.ap.name", wlcInfo.APName)
	}

	// thread.name - OTEL semantic convention for thread
	if wlcInfo.ThreadName != "" {
		attrs.PutStr("thread.name", wlcInfo.ThreadName)
	}

	// Code location (OTEL semantic conventions)
	if wlcInfo.CodeFilepath != "" {
		attrs.PutStr("code.filepath", wlcInfo.CodeFilepath)
	}
	if wlcInfo.CodeLineno != "" {
		attrs.PutStr("code.lineno", wlcInfo.CodeLineno)
	}

	// WiFi-specific fields
	// wifi.client.ap - The AP the client is connecting to/from
	if wlcInfo.ClientAP != "" {
		attrs.PutStr("wifi.client.ap", wlcInfo.ClientAP)
	}

	// wifi.radio - Radio band (2.4GHz, 5GHz)
	if wlcInfo.Radio != "" {
		attrs.PutStr("wifi.radio", wlcInfo.Radio)
	}

	// wifi.wlan.id - WLAN ID
	if wlcInfo.WLANId != "" {
		attrs.PutStr("wifi.wlan.id", wlcInfo.WLANId)
	}

	// Adjust severity based on mnemonic
	p.maybeAdjustCiscoSeverity(lr, attrs)

	// Rewrite body to clean message if configured
	if p.config.RewriteMessages && wlcInfo.CleanMessage != "" {
		lr.Body().SetStr(wlcInfo.CleanMessage)
	} else {
		p.maybeCleanBody(lr)
	}
}

// maybeSetHostname sets host.name from net.peer.name if configured and not already set
func (p *logsProcessor) maybeSetHostname(resourceAttrs pcommon.Map, attrs pcommon.Map) {
	if !p.config.SetHostname {
		return
	}

	// Skip if host.name already set
	if _, found := resourceAttrs.Get("host.name"); found {
		return
	}

	// Copy from net.peer.name if available
	if peerName, ok := attrs.Get("net.peer.name"); ok {
		resourceAttrs.PutStr("host.name", peerName.Str())
	}
}

// maybeCleanBody removes <priority> prefix from body if configured
func (p *logsProcessor) maybeCleanBody(lr plog.LogRecord) {
	if !p.config.CleanBody {
		return
	}

	if lr.Body().Type() != pcommon.ValueTypeStr {
		return
	}

	body := lr.Body().Str()
	cleaned := CleanBody(body)
	if cleaned != body {
		lr.Body().SetStr(cleaned)
	}
}

// maybeExtractInterface extracts network interface name from log message
// and looks up the port alias from the port database
func (p *logsProcessor) maybeExtractInterface(lr plog.LogRecord, attrs pcommon.Map, resourceAttrs pcommon.Map, sourceText string, hostname string) {
	if sourceText == "" {
		return
	}

	// Extract interface name
	ifName := ExtractInterfaceName(sourceText)
	if ifName == "" {
		return
	}

	// Set network.interface.name
	attrs.PutStr("network.interface.name", ifName)

	// Look up port alias if port database is available
	if p.portDB != nil && hostname != "" {
		if alias := p.portDB.Lookup(hostname, ifName); alias != "" {
			attrs.PutStr("network.interface.alias", alias)
		}
	}
}

// maybeAdjustCiscoSeverity adjusts severity based on Cisco mnemonic
func (p *logsProcessor) maybeAdjustCiscoSeverity(lr plog.LogRecord, attrs pcommon.Map) {
	if !p.config.AdjustSeverity {
		return
	}

	// Get mnemonic
	mnemonicVal, found := attrs.Get("cisco_mnemonic")
	if !found {
		return
	}
	mnemonic := mnemonicVal.Str()

	// Check for severity override based on mnemonic
	if override := GetSeverityOverride(mnemonic); override != nil {
		lr.SetSeverityNumber(override.Number)
		lr.SetSeverityText(override.Text)
		return
	}

	// If no override, convert Cisco severity to OTEL if not already set
	if lr.SeverityNumber() == plog.SeverityNumberUnspecified {
		sevVal, found := attrs.Get("cisco_severity")
		if found {
			number, text := CiscoSeverityToOTEL(sevVal.Str())
			if number != plog.SeverityNumberUnspecified {
				lr.SetSeverityNumber(number)
				lr.SetSeverityText(text)
			}
		}
	}
}
