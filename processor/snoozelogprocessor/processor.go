// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package snoozelogprocessor

import (
	"context"
	"regexp"
	"strings"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
)

// linePattern matches the first line only: HH:MM:SS LEVEL message.
var linePattern = regexp.MustCompile(`^\d{2}:\d{2}:\d{2}\s+([A-Z]+)\s+(.*)$`)

type snoozeProcessor struct {
	rules []MatchRule
	next  consumer.Logs
}

func (p *snoozeProcessor) Start(context.Context, component.Host) error { return nil }

func (p *snoozeProcessor) Shutdown(context.Context) error { return nil }

func (p *snoozeProcessor) Capabilities() consumer.Capabilities {
	return consumer.Capabilities{MutatesData: true}
}

func (p *snoozeProcessor) ConsumeLogs(ctx context.Context, ld plog.Logs) error {
	rls := ld.ResourceLogs()
	for i := 0; i < rls.Len(); i++ {
		rl := rls.At(i)
		if !p.matches(rl.Resource().Attributes()) {
			continue
		}
		for j := 0; j < rl.ScopeLogs().Len(); j++ {
			recs := rl.ScopeLogs().At(j).LogRecords()
			for k := 0; k < recs.Len(); k++ {
				parse(recs.At(k))
			}
		}
	}
	return p.next.ConsumeLogs(ctx, ld)
}

func (p *snoozeProcessor) matches(res pcommon.Map) bool {
	ns, ok := res.Get("k8s.namespace.name")
	if !ok {
		return false
	}
	container, ok := res.Get("k8s.container.name")
	if !ok {
		return false
	}
	for _, rule := range p.rules {
		if rule.Namespace == ns.Str() && rule.Container == container.Str() {
			return true
		}
	}
	return false
}

// parse sets severity from the first line and keeps any later lines in the body.
// The line carries no date, so the record timestamp is left alone (D-6).
func parse(lr plog.LogRecord) {
	if lr.Body().Type() != pcommon.ValueTypeStr {
		return
	}
	body := lr.Body().Str()
	first, rest, multiline := strings.Cut(body, "\n")

	m := linePattern.FindStringSubmatch(first)
	if m == nil {
		return
	}
	number, text, ok := severity(m[1])
	if !ok {
		return
	}
	lr.SetSeverityNumber(number)
	lr.SetSeverityText(text)

	message := m[2]
	if multiline {
		message += "\n" + rest
	}
	lr.Body().SetStr(message)
}

func severity(level string) (plog.SeverityNumber, string, bool) {
	switch level {
	case "DEBUG":
		return plog.SeverityNumberDebug, "DEBUG", true
	case "INFO":
		return plog.SeverityNumberInfo, "INFO", true
	case "WARNING", "WARN":
		return plog.SeverityNumberWarn, "WARN", true
	case "ERROR":
		return plog.SeverityNumberError, "ERROR", true
	case "CRITICAL", "FATAL":
		return plog.SeverityNumberFatal, "FATAL", true
	}
	return plog.SeverityNumberUnspecified, "", false
}
