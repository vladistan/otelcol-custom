// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package filterlogprocessor

import (
	"fmt"
	"regexp"
	"strings"
)

// FilterLogInfo holds parsed pfSense/OPNsense filterlog data
type FilterLogInfo struct {
	// Rule information
	RuleID      string // Rule number (e.g., "11")
	RuleTracker string // Rule tracker UUID

	// Interface and action
	Interface string // Network interface (em1, igb0)
	Reason    string // Match reason (match, bad-offset)
	Action    string // Firewall action (block, pass)
	Direction string // Packet direction (in, out)

	// IP layer
	IPVersion string // IP version (4, 6)
	TTL       string // Time-To-Live
	Protocol  string // Protocol name (tcp, udp, icmp)
	Length    string // Packet length

	// Addresses and ports
	SourceIP        string // Source IP address
	DestinationIP   string // Destination IP address
	SourcePort      string // Source port (TCP/UDP only)
	DestinationPort string // Destination port (TCP/UDP only)

	// TCP specific
	TCPFlags string // TCP flags (S, SA, A, F, R, P)

	// ICMP specific
	ICMPType string // ICMP type
	ICMPID   string // ICMP ID

	// Clean message for body rewrite
	CleanMessage string
}

// Regex to detect filterlog format in body
// Format: starts with rule number, followed by CSV fields
var filterlogRegex = regexp.MustCompile(`^\d+,`)

// IsFilterLog checks if the text looks like a filterlog entry
func IsFilterLog(text string) bool {
	return filterlogRegex.MatchString(text)
}

// ParseFilterLog parses a pfSense/OPNsense filterlog CSV entry.
// Returns nil if the text doesn't match filterlog format.
func ParseFilterLog(text string) *FilterLogInfo {
	if text == "" {
		return nil
	}

	// Must start with a digit (rule number)
	if !filterlogRegex.MatchString(text) {
		return nil
	}

	// Split by comma
	fields := strings.Split(text, ",")
	if len(fields) < 18 {
		return nil
	}

	info := &FilterLogInfo{
		RuleID:      fields[0],
		RuleTracker: fields[3],
		Interface:   fields[4],
		Reason:      fields[5],
		Action:      fields[6],
		Direction:   fields[7],
		IPVersion:   fields[8],
	}

	// Common IP fields (positions vary slightly between IPv4 and IPv6)
	switch info.IPVersion {
	case "4":
		// IPv4 format
		if len(fields) >= 19 {
			info.TTL = fields[11]
			info.Protocol = fields[16]
			info.Length = fields[17]
			info.SourceIP = fields[18]
		}
		if len(fields) >= 20 {
			info.DestinationIP = fields[19]
		}

		// Protocol-specific fields
		switch info.Protocol {
		case "tcp":
			if len(fields) >= 24 {
				info.SourcePort = fields[20]
				info.DestinationPort = fields[21]
				info.TCPFlags = fields[23]
			}
		case "udp":
			if len(fields) >= 22 {
				info.SourcePort = fields[20]
				info.DestinationPort = fields[21]
			}
		case "icmp":
			if len(fields) >= 22 {
				info.ICMPType = fields[20]
				info.ICMPID = fields[21]
			}
		}
	case "6":
		// IPv6 format: rule,subrule,anchor,tracker,interface,reason,action,direction,
		//              ipversion,class,flowlabel,hlim,proto,length,srcip,dstip,...
		if len(fields) >= 16 {
			info.Protocol = fields[12]
			info.Length = fields[13]
			info.SourceIP = fields[14]
			info.DestinationIP = fields[15]
		}

		// Protocol-specific fields for IPv6
		switch info.Protocol {
		case "tcp":
			if len(fields) >= 20 {
				info.SourcePort = fields[16]
				info.DestinationPort = fields[17]
				info.TCPFlags = fields[19]
			}
		case "udp":
			if len(fields) >= 18 {
				info.SourcePort = fields[16]
				info.DestinationPort = fields[17]
			}
		case "icmp", "ipv6-icmp":
			if len(fields) >= 18 {
				info.ICMPType = fields[16]
				info.ICMPID = fields[17]
			}
		}
	}

	// Build clean message
	info.CleanMessage = buildCleanMessage(info)

	return info
}

// buildCleanMessage creates a human-readable message from parsed filterlog
func buildCleanMessage(info *FilterLogInfo) string {
	// Convert direction
	direction := "inbound"
	if info.Direction == "out" {
		direction = "outbound"
	}

	// Format action (uppercase)
	action := strings.ToUpper(info.Action)

	// Build address:port strings
	var src, dst string
	if info.SourcePort != "" {
		src = fmt.Sprintf("%s:%s", info.SourceIP, info.SourcePort)
	} else {
		src = info.SourceIP
	}
	if info.DestinationPort != "" {
		dst = fmt.Sprintf("%s:%s", info.DestinationIP, info.DestinationPort)
	} else {
		dst = info.DestinationIP
	}

	// Format TCP flags
	flagsStr := ""
	if info.TCPFlags != "" {
		flagsStr = fmt.Sprintf(" [%s]", expandTCPFlags(info.TCPFlags))
	}

	// Format ICMP
	icmpStr := ""
	if info.ICMPType != "" {
		icmpStr = fmt.Sprintf(" type=%s", info.ICMPType)
		if info.ICMPID != "" {
			icmpStr += fmt.Sprintf(" id=%s", info.ICMPID)
		}
	}

	// Build message
	// BLOCK em1 inbound tcp 91.148.190.150:50406 → 24.2.177.2:47574 [SYN] (rule 11)
	return fmt.Sprintf("%s %s %s %s %s → %s%s%s (rule %s)",
		action, info.Interface, direction, info.Protocol,
		src, dst, flagsStr, icmpStr, info.RuleID)
}

// expandTCPFlags converts single-letter flags to readable names
func expandTCPFlags(flags string) string {
	var expanded []string
	for _, f := range flags {
		switch f {
		case 'S':
			expanded = append(expanded, "SYN")
		case 'A':
			expanded = append(expanded, "ACK")
		case 'F':
			expanded = append(expanded, "FIN")
		case 'R':
			expanded = append(expanded, "RST")
		case 'P':
			expanded = append(expanded, "PSH")
		case 'U':
			expanded = append(expanded, "URG")
		case 'E':
			expanded = append(expanded, "ECE")
		case 'W':
			expanded = append(expanded, "CWR")
		default:
			expanded = append(expanded, string(f))
		}
	}
	return strings.Join(expanded, ",")
}

// NormalizeDirection converts pf direction to OTEL convention
func NormalizeDirection(dir string) string {
	switch dir {
	case "in":
		return "inbound"
	case "out":
		return "outbound"
	default:
		return dir
	}
}

// NormalizeIPVersion converts version number to OTEL network.type
func NormalizeIPVersion(version string) string {
	switch version {
	case "4":
		return "ipv4"
	case "6":
		return "ipv6"
	default:
		return version
	}
}
