// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package ipextractorprocessor

import (
	"net"
	"regexp"
	"strings"
)

// IPv4 pattern - matches standard dotted decimal notation
// Uses word boundaries to avoid matching partial numbers
// Captures 4 octets (0-255 each)
var ipv4Pattern = regexp.MustCompile(`\b((?:(?:25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)\.){3}(?:25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?))\b`)

// IPv6 patterns - various formats
// Full form: 2001:0db8:85a3:0000:0000:8a2e:0370:7334
// Compressed: 2001:db8:85a3::8a2e:370:7334
// With zone: fe80::1%eth0
var ipv6Pattern = regexp.MustCompile(`\b(` +
	// Full or partially compressed IPv6
	`(?:[0-9a-fA-F]{1,4}:){7}[0-9a-fA-F]{1,4}|` + // Full
	`(?:[0-9a-fA-F]{1,4}:){1,7}:|` + // Ends with ::
	`(?:[0-9a-fA-F]{1,4}:){1,6}:[0-9a-fA-F]{1,4}|` + // :: in middle (1)
	`(?:[0-9a-fA-F]{1,4}:){1,5}(?::[0-9a-fA-F]{1,4}){1,2}|` + // :: in middle (2)
	`(?:[0-9a-fA-F]{1,4}:){1,4}(?::[0-9a-fA-F]{1,4}){1,3}|` + // :: in middle (3)
	`(?:[0-9a-fA-F]{1,4}:){1,3}(?::[0-9a-fA-F]{1,4}){1,4}|` + // :: in middle (4)
	`(?:[0-9a-fA-F]{1,4}:){1,2}(?::[0-9a-fA-F]{1,4}){1,5}|` + // :: in middle (5)
	`[0-9a-fA-F]{1,4}:(?::[0-9a-fA-F]{1,4}){1,6}|` + // :: in middle (6)
	`:(?::[0-9a-fA-F]{1,4}){1,7}|` + // Starts with ::
	`::` + // Just ::
	`)(?:%[a-zA-Z0-9]+)?\b`) // Optional zone ID

// Common IPs to exclude (loopback, unspecified)
var excludedIPs = map[string]bool{
	"0.0.0.0":   true,
	"127.0.0.1": true,
	"::":        true,
	"::1":       true,
}

// ExtractIPs extracts all IP addresses from a string.
// Returns normalized IPs (lowercase for IPv6).
func ExtractIPs(input string, includeIPv6 bool) []string {
	if input == "" {
		return nil
	}

	var ips []string
	seen := make(map[string]bool)

	// Extract IPv4 addresses
	matches := ipv4Pattern.FindAllString(input, -1)
	for _, match := range matches {
		// Validate it's actually a valid IP
		if ip := net.ParseIP(match); ip != nil {
			normalized := ip.String()
			if !excludedIPs[normalized] && !seen[normalized] {
				seen[normalized] = true
				ips = append(ips, normalized)
			}
		}
	}

	// Extract IPv6 addresses if enabled
	if includeIPv6 {
		matches := ipv6Pattern.FindAllString(input, -1)
		for _, match := range matches {
			// Remove zone ID for parsing
			cleanIP := match
			if idx := strings.Index(match, "%"); idx != -1 {
				cleanIP = match[:idx]
			}
			if ip := net.ParseIP(cleanIP); ip != nil {
				normalized := strings.ToLower(ip.String())
				if !excludedIPs[normalized] && !seen[normalized] {
					seen[normalized] = true
					ips = append(ips, normalized)
				}
			}
		}
	}

	return ips
}

// ExtractFirstIP extracts the first IP address found in a string.
// Returns empty string if no IP found.
func ExtractFirstIP(input string, includeIPv6 bool) string {
	ips := ExtractIPs(input, includeIPv6)
	if len(ips) > 0 {
		return ips[0]
	}
	return ""
}

// IsValidIPv4 checks if a string is a valid IPv4 address.
func IsValidIPv4(ip string) bool {
	parsed := net.ParseIP(ip)
	return parsed != nil && parsed.To4() != nil
}

// IsValidIPv6 checks if a string is a valid IPv6 address.
func IsValidIPv6(ip string) bool {
	parsed := net.ParseIP(ip)
	return parsed != nil && parsed.To4() == nil
}
