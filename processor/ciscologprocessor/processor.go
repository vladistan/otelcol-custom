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

// ConsumeLogs splits records by the host.name and service.name they end with, so no record takes
// the host of another.
func (p *logsProcessor) ConsumeLogs(ctx context.Context, ld plog.Logs) error {
	return p.nextConsumer.ConsumeLogs(ctx, processPerResource(ld, p.processLogRecord))
}

func (p *logsProcessor) processLogRecord(lr plog.LogRecord, resourceAttrs pcommon.Map) {
	attrs := lr.Attributes()

	var sourceText string
	if val, ok := attrs.Get(p.config.SourceAttribute); ok {
		sourceText = val.Str()
	}
	if sourceText == "" && lr.Body().Type() == pcommon.ValueTypeStr {
		sourceText = lr.Body().Str()
	}

	hostname := ""
	if val, ok := resourceAttrs.Get("host.name"); ok {
		hostname = val.Str()
	}

	// transform/syslog already parsed the line.
	if _, found := attrs.Get("cisco_facility"); found {
		p.maybeSetHostname(resourceAttrs, attrs)
		p.maybeExtractInterface(attrs, sourceText, hostname)
		p.maybeAdjustCiscoSeverity(lr, attrs)
		p.maybeCleanBody(lr)
		return
	}

	if sourceText == "" {
		return
	}

	if info := ParseCiscoMnemonic(sourceText); info != nil {
		attrs.PutStr("cisco_facility", info.Facility)
		attrs.PutStr("cisco_mnemonic", info.Mnemonic)
		attrs.PutStr("cisco_severity", info.Severity)

		if _, found := resourceAttrs.Get("service.name"); !found {
			resourceAttrs.PutStr("service.name", info.Facility)
		}

		if wlcInfo := ParseWLCLog(sourceText); wlcInfo != nil {
			p.processWLCLog(lr, attrs, resourceAttrs, wlcInfo)
			return
		}

		p.maybeSetHostname(resourceAttrs, attrs)

		// maybeSetHostname may have set it; the port lookup needs the new value.
		if hostname == "" {
			if val, ok := resourceAttrs.Get("host.name"); ok {
				hostname = val.Str()
			}
		}

		p.maybeExtractInterface(attrs, sourceText, hostname)
		p.maybeAdjustCiscoSeverity(lr, attrs)
		p.maybeCleanBody(lr)
		return
	}

	if info := ParseHPLog(sourceText); info != nil {
		p.processHPLog(lr, attrs, resourceAttrs, info)
		return
	}

	p.maybeSetHostname(resourceAttrs, attrs)
	p.maybeCleanBody(lr)
}

func (p *logsProcessor) processHPLog(lr plog.LogRecord, attrs pcommon.Map, resourceAttrs pcommon.Map, info *HPLogInfo) {
	if _, found := resourceAttrs.Get("host.name"); !found {
		resourceAttrs.PutStr("host.name", info.Hostname)
	}
	if _, found := resourceAttrs.Get("service.name"); !found {
		resourceAttrs.PutStr("service.name", info.AppName)
	}
	putNonEmpty(attrs, "process.name", info.ProcessID)
	putNonEmpty(attrs, "code.filepath", info.CodeFilepath)
	putNonEmpty(attrs, "code.lineno", info.CodeLineno)
	putNonEmpty(attrs, "log.sequence", info.LogSequence)

	if p.config.RewriteMessages && info.CleanMessage != "" {
		lr.Body().SetStr(info.CleanMessage)
	} else {
		p.maybeCleanBody(lr)
	}
}

func (p *logsProcessor) processWLCLog(lr plog.LogRecord, attrs pcommon.Map, resourceAttrs pcommon.Map, wlcInfo *WLCLogInfo) {
	p.maybeSetHostname(resourceAttrs, attrs)

	putNonEmpty(attrs, "wifi.ap.name", wlcInfo.APName)
	putNonEmpty(attrs, "thread.name", wlcInfo.ThreadName)
	putNonEmpty(attrs, "code.filepath", wlcInfo.CodeFilepath)
	putNonEmpty(attrs, "code.lineno", wlcInfo.CodeLineno)
	putNonEmpty(attrs, "wifi.client.ap", wlcInfo.ClientAP)
	putNonEmpty(attrs, "wifi.radio", wlcInfo.Radio)
	putNonEmpty(attrs, "wifi.wlan.id", wlcInfo.WLANId)

	p.maybeAdjustCiscoSeverity(lr, attrs)

	if p.config.RewriteMessages && wlcInfo.CleanMessage != "" {
		lr.Body().SetStr(wlcInfo.CleanMessage)
	} else {
		p.maybeCleanBody(lr)
	}
}

func putNonEmpty(attrs pcommon.Map, key, value string) {
	if value != "" {
		attrs.PutStr(key, value)
	}
}

func (p *logsProcessor) maybeSetHostname(resourceAttrs pcommon.Map, attrs pcommon.Map) {
	if !p.config.SetHostname {
		return
	}
	if _, found := resourceAttrs.Get("host.name"); found {
		return
	}
	if peerName, ok := attrs.Get("net.peer.name"); ok {
		resourceAttrs.PutStr("host.name", peerName.Str())
	}
}

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

func (p *logsProcessor) maybeExtractInterface(attrs pcommon.Map, sourceText string, hostname string) {
	if sourceText == "" {
		return
	}
	ifName := ExtractInterfaceName(sourceText)
	if ifName == "" {
		return
	}
	attrs.PutStr("network.interface.name", ifName)

	if p.portDB != nil && hostname != "" {
		if alias := p.portDB.Lookup(hostname, ifName); alias != "" {
			attrs.PutStr("network.interface.alias", alias)
		}
	}
}

func (p *logsProcessor) maybeAdjustCiscoSeverity(lr plog.LogRecord, attrs pcommon.Map) {
	if !p.config.AdjustSeverity {
		return
	}
	mnemonicVal, found := attrs.Get("cisco_mnemonic")
	if !found {
		return
	}

	if override := GetSeverityOverride(mnemonicVal.Str()); override != nil {
		lr.SetSeverityNumber(override.Number)
		lr.SetSeverityText(override.Text)
		return
	}

	// No override: keep a severity already set, else convert the Cisco one.
	if lr.SeverityNumber() == plog.SeverityNumberUnspecified {
		if sevVal, found := attrs.Get("cisco_severity"); found {
			number, text := CiscoSeverityToOTEL(sevVal.Str())
			if number != plog.SeverityNumberUnspecified {
				lr.SetSeverityNumber(number)
				lr.SetSeverityText(text)
			}
		}
	}
}
