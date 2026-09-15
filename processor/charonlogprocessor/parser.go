// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package charonlogprocessor

import (
	"regexp"
	"strings"
)

// CharonLogInfo holds parsed information from a strongSwan charon log entry.
type CharonLogInfo struct {
	ThreadID      string            // Thread number (e.g., "13")
	Component     string            // Component: IKE, NET, ENC, CFG, CHD, JOB, KNL, MGR, etc.
	ConnectionID  string            // Connection UUID
	ConnectionSeq string            // Connection sequence number
	Message       string            // Human-readable message
	Extra         map[string]string // Additional parsed fields
}

var (
	// Main charon log pattern: ThreadID[Component] <ConnectionID|Seq> message
	// Example: 13[IKE] <efd7edbd-4bdf-46bd-b17c-c1d569b4c5a7|82> CHILD_SA ... closed
	charonPattern = regexp.MustCompile(`^(\d+)\[(\w+)\]\s+<([^|>]+)\|?(\d*)>\s+(.*)$`)

	// Packet pattern: received/sending packet: from IP[port] to IP[port] (bytes)
	packetPattern = regexp.MustCompile(`(received|sending) packet: from ([\d.]+)\[(\d+)\] to ([\d.]+)\[(\d+)\] \((\d+) bytes\)`)

	// CHILD_SA pattern: CHILD_SA uuid{num} established/closed/...
	childSAPattern = regexp.MustCompile(`CHILD_SA\s+([a-f0-9-]+)\{(\d+)\}\s+(\w+)`)

	// SPI pattern: SPIs abc123_i def456_o
	spiPattern = regexp.MustCompile(`SPIs?\s+([a-f0-9]+)_i\s+([a-f0-9]+)_o`)

	// Traffic selector pattern: TS subnet === subnet subnet ...
	tsPattern = regexp.MustCompile(`TS\s+([\d./\s=]+)$`)

	// Proposal pattern: selected proposal: ESP:algo/...
	proposalPattern = regexp.MustCompile(`selected proposal:\s+(.+)$`)

	// DELETE pattern: received DELETE for ESP CHILD_SA with SPI xxx
	deletePattern = regexp.MustCompile(`DELETE for (\w+) CHILD_SA with SPI ([a-f0-9]+)`)

	// Authentication pattern: authentication of 'identity' with ... successful
	authPattern = regexp.MustCompile(`authentication of '([^']+)' with (\S+) (successful|failed)`)

	// IKE_SA pattern: IKE_SA name[num] established/...
	ikeSAPattern = regexp.MustCompile(`IKE_SA\s+(\S+)\[(\d+)\]\s+(\w+)`)
)

// IsCharonLog checks if a log line looks like a charon log entry.
func IsCharonLog(body string) bool {
	if len(body) < 10 {
		return false
	}
	// Quick check: must start with digit followed by [
	if body[0] < '0' || body[0] > '9' {
		return false
	}
	// Look for the [COMPONENT] pattern
	bracketIdx := strings.Index(body, "[")
	if bracketIdx < 1 || bracketIdx > 3 {
		return false
	}
	closeBracket := strings.Index(body[bracketIdx:], "]")
	if closeBracket < 2 || closeBracket > 5 {
		return false
	}
	// Look for the <connection> pattern
	if !strings.Contains(body, "<") || !strings.Contains(body, ">") {
		return false
	}
	return charonPattern.MatchString(body)
}

// ParseCharonLog parses a charon log entry and extracts structured information.
func ParseCharonLog(body string) *CharonLogInfo {
	matches := charonPattern.FindStringSubmatch(body)
	if matches == nil {
		return nil
	}

	info := &CharonLogInfo{
		ThreadID:      matches[1],
		Component:     matches[2],
		ConnectionID:  matches[3],
		ConnectionSeq: matches[4],
		Message:       matches[5],
		Extra:         make(map[string]string),
	}

	msg := matches[5]

	// Extract packet info
	if packetMatch := packetPattern.FindStringSubmatch(msg); packetMatch != nil {
		info.Extra["ipsec.direction"] = packetMatch[1]
		if packetMatch[1] == "received" {
			info.Extra["ipsec.remote_ip"] = packetMatch[2]
			info.Extra["ipsec.remote_port"] = packetMatch[3]
			info.Extra["ipsec.local_ip"] = packetMatch[4]
			info.Extra["ipsec.local_port"] = packetMatch[5]
		} else {
			info.Extra["ipsec.local_ip"] = packetMatch[2]
			info.Extra["ipsec.local_port"] = packetMatch[3]
			info.Extra["ipsec.remote_ip"] = packetMatch[4]
			info.Extra["ipsec.remote_port"] = packetMatch[5]
		}
		info.Extra["ipsec.packet_size"] = packetMatch[6]
		// Simplify message
		info.Message = packetMatch[1] + " packet " + packetMatch[6] + " bytes " + info.Extra["ipsec.remote_ip"]
	}

	// Extract CHILD_SA info
	if childMatch := childSAPattern.FindStringSubmatch(msg); childMatch != nil {
		info.Extra["ipsec.child_sa"] = childMatch[1]
		info.Extra["ipsec.child_sa_num"] = childMatch[2]
		info.Extra["ipsec.child_sa_state"] = childMatch[3]
	}

	// Extract SPI info
	if spiMatch := spiPattern.FindStringSubmatch(msg); spiMatch != nil {
		info.Extra["ipsec.spi_in"] = spiMatch[1]
		info.Extra["ipsec.spi_out"] = spiMatch[2]
	}

	// Extract traffic selectors
	if tsMatch := tsPattern.FindStringSubmatch(msg); tsMatch != nil {
		info.Extra["ipsec.traffic_selectors"] = strings.TrimSpace(tsMatch[1])
	}

	// Extract proposal
	if proposalMatch := proposalPattern.FindStringSubmatch(msg); proposalMatch != nil {
		info.Extra["ipsec.proposal"] = proposalMatch[1]
		info.Message = "selected proposal: " + proposalMatch[1]
	}

	// Extract DELETE info
	if deleteMatch := deletePattern.FindStringSubmatch(msg); deleteMatch != nil {
		info.Extra["ipsec.delete_protocol"] = deleteMatch[1]
		info.Extra["ipsec.delete_spi"] = deleteMatch[2]
	}

	// Extract authentication info
	if authMatch := authPattern.FindStringSubmatch(msg); authMatch != nil {
		info.Extra["ipsec.auth_identity"] = authMatch[1]
		info.Extra["ipsec.auth_method"] = authMatch[2]
		info.Extra["ipsec.auth_result"] = authMatch[3]
	}

	// Extract IKE_SA info
	if ikeMatch := ikeSAPattern.FindStringSubmatch(msg); ikeMatch != nil {
		info.Extra["ipsec.ike_sa_name"] = ikeMatch[1]
		info.Extra["ipsec.ike_sa_num"] = ikeMatch[2]
		info.Extra["ipsec.ike_sa_state"] = ikeMatch[3]
	}

	return info
}

// ComponentToDescription returns a human-readable description of a charon component.
func ComponentToDescription(component string) string {
	switch component {
	case "IKE":
		return "IKE Protocol"
	case "NET":
		return "Network"
	case "ENC":
		return "Encoding"
	case "CFG":
		return "Configuration"
	case "CHD":
		return "Child SA"
	case "JOB":
		return "Job Scheduler"
	case "KNL":
		return "Kernel Interface"
	case "MGR":
		return "SA Manager"
	case "ASN":
		return "ASN.1 Parser"
	case "LIB":
		return "Library"
	case "TLS":
		return "TLS"
	case "ESP":
		return "ESP Protocol"
	case "AH":
		return "AH Protocol"
	default:
		return component
	}
}
