// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package ciscologprocessor

import "go.opentelemetry.io/collector/pdata/plog"

// SeverityOverride holds the adjusted severity for specific mnemonics.
type SeverityOverride struct {
	Number plog.SeverityNumber
	Text   string
}

// severityOverrides maps Cisco mnemonics to adjusted severity levels.
// Interface state changes and topology events should be WARN, not ERROR.
var severityOverrides = map[string]SeverityOverride{
	// Interface state changes (typically sev 3/ERROR, should be WARN)
	"UPDOWN":     {plog.SeverityNumberWarn, "WARN"},
	"CHANGED":    {plog.SeverityNumberWarn, "WARN"},
	"PORTSTATUS": {plog.SeverityNumberWarn, "WARN"},
	"LINK_DOWN":  {plog.SeverityNumberWarn, "WARN"},
	"LINK_UP":    {plog.SeverityNumberWarn, "WARN"},

	// Spanning tree topology changes
	"TOPOLOGY_CHANGE":         {plog.SeverityNumberWarn, "WARN"},
	"TOPOLOGY_CHANGE_RCVD":    {plog.SeverityNumberWarn, "WARN"},
	"TOPOTRAP":                {plog.SeverityNumberWarn, "WARN"},
	"ROOT_BRIDGE_CHANGE":      {plog.SeverityNumberWarn, "WARN"},
	"BLOCK":                   {plog.SeverityNumberWarn, "WARN"},
	"UNBLOCK":                 {plog.SeverityNumberWarn, "WARN"},
	"PORT_ROLE":               {plog.SeverityNumberWarn, "WARN"},
	"PORT_STATE":              {plog.SeverityNumberWarn, "WARN"},
	"SPANTREE_PORT_STATE":     {plog.SeverityNumberWarn, "WARN"},
	"FWD_CHANGE":              {plog.SeverityNumberWarn, "WARN"},
	"LEARN_CHANGE":            {plog.SeverityNumberWarn, "WARN"},
	"LISTENING_TO_FORWARDING": {plog.SeverityNumberWarn, "WARN"},

	// MAC flapping is WARN (network issue, but not error)
	"MACFLAP_NOTIF": {plog.SeverityNumberWarn, "WARN"},
	"MAC_FLAP":      {plog.SeverityNumberWarn, "WARN"},

	// Client association/disassociation (WLC) - informational
	"CLIENT_ASSOCIATE":    {plog.SeverityNumberInfo, "INFO"},
	"CLIENT_DISASSOCIATE": {plog.SeverityNumberInfo, "INFO"},
	"CLIENT_DEAUTH":       {plog.SeverityNumberWarn, "WARN"},

	// PoE events
	"POE_ALLOCATED": {plog.SeverityNumberInfo, "INFO"},
	"POE_REMOVED":   {plog.SeverityNumberInfo, "INFO"},
	"POE_OVERLOAD":  {plog.SeverityNumberWarn, "WARN"},
}

// GetSeverityOverride returns the adjusted severity for a mnemonic.
// Returns nil if no override exists for the mnemonic.
func GetSeverityOverride(mnemonic string) *SeverityOverride {
	if override, ok := severityOverrides[mnemonic]; ok {
		return &override
	}
	return nil
}

// CiscoSeverityToOTEL converts Cisco severity string to OTEL severity.
// Cisco uses 0-7 (Emergency to Debug) or letters (N=Notice, I=Info, W=Warn, E=Error).
func CiscoSeverityToOTEL(severity string) (plog.SeverityNumber, string) {
	switch severity {
	case "0": // Emergency
		return plog.SeverityNumberFatal4, "FATAL"
	case "1": // Alert
		return plog.SeverityNumberFatal, "FATAL"
	case "2": // Critical
		return plog.SeverityNumberError2, "ERROR"
	case "3", "E": // Error
		return plog.SeverityNumberError, "ERROR"
	case "4", "W": // Warning
		return plog.SeverityNumberWarn, "WARN"
	case "5", "N": // Notice
		return plog.SeverityNumberInfo2, "INFO"
	case "6", "I": // Informational
		return plog.SeverityNumberInfo, "INFO"
	case "7", "D": // Debug
		return plog.SeverityNumberDebug, "DEBUG"
	default:
		return plog.SeverityNumberUnspecified, ""
	}
}
