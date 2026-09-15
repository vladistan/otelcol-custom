// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package kernellogprocessor

import (
	"regexp"
	"strings"
)

// KernelLogInfo holds parsed information from a kernel log entry.
type KernelLogInfo struct {
	Subsystem string            // e.g., "sd", "IPVS", "cni0", "em0", "NET"
	Device    string            // e.g., "sdd", "em0", "veth1e60a14b"
	Message   string            // Human-readable message
	Severity  string            // Extracted severity: ERROR, WARN, INFO
	ErrorType string            // e.g., "Medium Error", "critical medium error"
	Extra     map[string]string // Additional parsed fields
}

var (
	// SCSI disk error: sd 11:0:0:0: [sdd] tag#0 FAILED Result: ...
	scsiPattern = regexp.MustCompile(`^sd\s+(\d+:\d+:\d+:\d+):\s+\[(\w+)\]\s+(.*)$`)

	// Critical medium error: critical medium error, dev sdd, sector 0 ...
	criticalMediumPattern = regexp.MustCompile(`^critical medium error,\s+dev\s+(\w+),\s+sector\s+(\d+)\s+(.*)$`)

	// IPVS message: IPVS: rr: TCP 192.0.2.1:4318 - no destination available
	ipvsPattern = regexp.MustCompile(`^IPVS:\s+(\w+):\s+(\w+)\s+([\d.:]+)\s+-\s+(.*)$`)

	// Network interface: em0: promiscuous mode enabled/disabled
	// cni0: port 6(veth...) entered blocking/disabled state
	netIfacePattern = regexp.MustCompile(`^(\w+):\s+(.*)$`)

	// veth events: veth1e60a14b: entered promiscuous mode
	vethPattern = regexp.MustCompile(`^(veth\w+):\s+(.*)$`)

	// FreeBSD kernel with timestamp: [230890] em0: promiscuous mode disabled
	bsdTimestampPattern = regexp.MustCompile(`^\[(\d+)\]\s*(.*)$`)

	// Sense Key pattern in SCSI errors
	senseKeyPattern = regexp.MustCompile(`Sense Key\s*:\s*([^[]+)`)

	// Add. Sense pattern
	addSensePattern = regexp.MustCompile(`Add\.\s*Sense:\s*(.+)`)

	// Rate limit: net_ratelimit: 21 callbacks suppressed
	rateLimitPattern = regexp.MustCompile(`^net_ratelimit:\s+(\d+)\s+callbacks suppressed$`)

	// clocksource: Long readout interval, skipping watchdog check
	clocksourcePattern = regexp.MustCompile(`^clocksource:\s+(.*)$`)

	// strongSwan/IPsec messages: 14[NET] <uuid|1> sending packet: from IP[port] to IP[port]
	ipsecPattern = regexp.MustCompile(`^\d+\[(\w+)\]\s+<([^>]+)>\s+(.*)$`)
)

// IsKernelLog checks if a log line looks like a kernel log entry.
func IsKernelLog(body string) bool {
	if len(body) < 3 {
		return false
	}

	// Check for common kernel log patterns
	// SCSI: sd X:X:X:X:
	if strings.HasPrefix(body, "sd ") {
		return true
	}

	// critical medium error
	if strings.HasPrefix(body, "critical medium error") {
		return true
	}

	// IPVS messages
	if strings.HasPrefix(body, "IPVS:") {
		return true
	}

	// Rate limit messages
	if strings.HasPrefix(body, "net_ratelimit:") {
		return true
	}

	// clocksource messages
	if strings.HasPrefix(body, "clocksource:") {
		return true
	}

	// BSD kernel with timestamp [123]
	if body[0] == '[' && len(body) > 5 {
		// Look for closing bracket
		for i := 1; i < len(body) && i < 15; i++ {
			if body[i] == ']' {
				return true
			}
			if body[i] < '0' || body[i] > '9' {
				break
			}
		}
	}

	// Network interface messages (em0:, cni0:, veth...)
	if netIfacePattern.MatchString(body) {
		// Make sure it's not a different format
		if !strings.Contains(body, "=") || strings.HasPrefix(body, "cni") || strings.HasPrefix(body, "veth") {
			return true
		}
	}

	// strongSwan/IPsec pattern
	if len(body) > 3 && body[0] >= '0' && body[0] <= '9' && strings.Contains(body, "[") {
		if ipsecPattern.MatchString(body) {
			return true
		}
	}

	return false
}

// ParseKernelLog parses a kernel log entry and extracts structured information.
func ParseKernelLog(body string) *KernelLogInfo {
	info := &KernelLogInfo{
		Message: body,
		Extra:   make(map[string]string),
	}

	// Strip BSD timestamp prefix if present
	if body[0] == '[' {
		if matches := bsdTimestampPattern.FindStringSubmatch(body); matches != nil {
			info.Extra["kernel.uptime"] = matches[1]
			body = strings.TrimSpace(matches[2])
			info.Message = body
		}
	}

	// Try to match SCSI disk errors
	if matches := scsiPattern.FindStringSubmatch(body); matches != nil {
		info.Subsystem = "scsi"
		info.Extra["scsi.address"] = matches[1]
		info.Device = matches[2]
		remainder := matches[3]

		// Check for FAILED
		if strings.Contains(remainder, "FAILED") {
			info.Severity = "ERROR"
			info.ErrorType = "SCSI Command Failed"
		}

		// Extract Sense Key
		if senseMatch := senseKeyPattern.FindStringSubmatch(remainder); senseMatch != nil {
			senseKey := strings.TrimSpace(senseMatch[1])
			info.Extra["scsi.sense_key"] = senseKey
			if senseKey == "Medium Error" {
				info.Severity = "ERROR"
				info.ErrorType = "Medium Error"
			}
		}

		// Extract Additional Sense
		if addSenseMatch := addSensePattern.FindStringSubmatch(remainder); addSenseMatch != nil {
			info.Extra["scsi.add_sense"] = strings.TrimSpace(addSenseMatch[1])
		}

		// Clean up message
		info.Message = "SCSI error on " + info.Device + ": " + summarizeSCSIError(remainder)
		return info
	}

	// Try critical medium error
	if matches := criticalMediumPattern.FindStringSubmatch(body); matches != nil {
		info.Subsystem = "block"
		info.Device = matches[1]
		info.Extra["block.sector"] = matches[2]
		info.Severity = "ERROR"
		info.ErrorType = "Critical Medium Error"
		info.Message = "Critical medium error on " + info.Device + " sector " + matches[2]
		return info
	}

	// Try IPVS pattern
	if matches := ipvsPattern.FindStringSubmatch(body); matches != nil {
		info.Subsystem = "ipvs"
		info.Extra["ipvs.scheduler"] = matches[1]
		info.Extra["ipvs.protocol"] = matches[2]
		info.Extra["ipvs.destination"] = matches[3]
		info.Message = "IPVS " + matches[2] + " " + matches[3] + ": " + matches[4]
		if strings.Contains(matches[4], "no destination") {
			info.Severity = "WARN"
			info.ErrorType = "No Destination"
		}
		return info
	}

	// Try rate limit pattern
	if matches := rateLimitPattern.FindStringSubmatch(body); matches != nil {
		info.Subsystem = "net"
		info.Extra["net.suppressed_count"] = matches[1]
		info.Message = matches[1] + " network callbacks suppressed"
		return info
	}

	// Try clocksource pattern
	if matches := clocksourcePattern.FindStringSubmatch(body); matches != nil {
		info.Subsystem = "clocksource"
		info.Message = "Clocksource: " + matches[1]
		return info
	}

	// Try strongSwan/IPsec pattern
	if matches := ipsecPattern.FindStringSubmatch(body); matches != nil {
		info.Subsystem = "ipsec"
		info.Extra["ipsec.component"] = matches[1]
		info.Extra["ipsec.connection"] = matches[2]
		info.Message = "IPsec " + matches[1] + ": " + matches[3]
		return info
	}

	// Try veth pattern
	if matches := vethPattern.FindStringSubmatch(body); matches != nil {
		info.Subsystem = "net"
		info.Device = matches[1]
		info.Message = matches[1] + ": " + matches[2]
		return info
	}

	// Try network interface pattern (more generic)
	if matches := netIfacePattern.FindStringSubmatch(body); matches != nil {
		device := matches[1]
		msg := matches[2]

		// Filter out non-kernel interface messages
		if isNetworkDevice(device) {
			info.Subsystem = "net"
			info.Device = device
			info.Message = device + ": " + msg

			// Check for port state changes
			if strings.Contains(msg, "entered blocking state") ||
				strings.Contains(msg, "entered disabled state") {
				info.Extra["net.port_state"] = extractPortState(msg)
			}
			if strings.Contains(msg, "promiscuous mode") {
				if strings.Contains(msg, "enabled") {
					info.Extra["net.promiscuous"] = "enabled"
				} else if strings.Contains(msg, "disabled") {
					info.Extra["net.promiscuous"] = "disabled"
				}
			}
			return info
		}
	}

	return info
}

// isNetworkDevice checks if a name looks like a network device.
func isNetworkDevice(name string) bool {
	prefixes := []string{
		"eth", "em", "eno", "enp", "ens", "enx", // Linux/BSD ethernet
		"wlan", "wlp", "wifi", // Wireless
		"cni", "docker", "veth", "br", // Container/bridge
		"lo", "virbr", "tun", "tap", // Virtual
		"bond", "team", // Bonding
	}
	for _, p := range prefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	// Single interface names like em0, em1
	if len(name) >= 2 && len(name) <= 6 {
		// Check if it ends with a digit
		if name[len(name)-1] >= '0' && name[len(name)-1] <= '9' {
			return true
		}
	}
	return false
}

// extractPortState extracts port state from messages like "port 6(veth...) entered blocking state"
func extractPortState(msg string) string {
	if strings.Contains(msg, "blocking") {
		return "blocking"
	}
	if strings.Contains(msg, "disabled") {
		return "disabled"
	}
	if strings.Contains(msg, "forwarding") {
		return "forwarding"
	}
	return ""
}

// summarizeSCSIError creates a short summary of a SCSI error.
func summarizeSCSIError(msg string) string {
	if strings.Contains(msg, "Medium Error") {
		return "Medium Error (disk read failure)"
	}
	if strings.Contains(msg, "FAILED") {
		return "Command Failed"
	}
	if strings.Contains(msg, "Unrecovered read error") {
		return "Unrecovered read error"
	}
	// Truncate long messages
	if len(msg) > 60 {
		return msg[:57] + "..."
	}
	return msg
}

// SeverityFromKernelLog returns OTEL severity based on parsed kernel log.
func SeverityFromKernelLog(info *KernelLogInfo) (int, string) {
	switch info.Severity {
	case "ERROR":
		return 17, "ERROR"
	case "WARN":
		return 13, "WARN"
	default:
		return 9, "INFO"
	}
}
