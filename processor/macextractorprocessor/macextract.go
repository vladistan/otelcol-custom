// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package macextractorprocessor

import (
	"regexp"
	"strings"
)

// MAC address patterns - each captures exactly 12 hex digits in different formats.
// We use word boundaries and negative lookahead/lookbehind (via context) to avoid
// matching partial strings or IPv6 addresses.
var (
	// Standard format: aa:bb:cc:dd:ee:ff or AA:BB:CC:DD:EE:FF
	// Uses word boundary to avoid matching partial strings
	macColonPattern = regexp.MustCompile(`(?i)\b([0-9a-f]{2}:[0-9a-f]{2}:[0-9a-f]{2}:[0-9a-f]{2}:[0-9a-f]{2}:[0-9a-f]{2})\b`)

	// Dashed format: aa-bb-cc-dd-ee-ff or AA-BB-CC-DD-EE-FF
	// Common in Windows network configurations
	macDashPattern = regexp.MustCompile(`(?i)\b([0-9a-f]{2}-[0-9a-f]{2}-[0-9a-f]{2}-[0-9a-f]{2}-[0-9a-f]{2}-[0-9a-f]{2})\b`)

	// Cisco format: aabb.ccdd.eeff or AABB.CCDD.EEFF
	// Three groups of 4 hex digits separated by dots
	// Note: This could potentially match part of an IP address in unusual contexts,
	// but IP addresses don't use hex digits a-f, and IPv6 uses colons not dots
	macCiscoPattern = regexp.MustCompile(`(?i)\b([0-9a-f]{4}\.[0-9a-f]{4}\.[0-9a-f]{4})\b`)

	// DHCPv6 DUID-LL pattern: 00:03:00:01:XX:XX:XX:XX:XX:XX
	// Type 3 (DUID-LL) + Hardware type 1 (Ethernet) + 6-byte MAC
	// Captures the MAC portion (last 6 octets)
	duidLLPattern = regexp.MustCompile(`(?i)\b00:03:00:01:([0-9a-f]{2}:[0-9a-f]{2}:[0-9a-f]{2}:[0-9a-f]{2}:[0-9a-f]{2}:[0-9a-f]{2})\b`)

	// DHCPv6 DUID-LLT pattern: 00:01:00:01:TT:TT:TT:TT:XX:XX:XX:XX:XX:XX
	// Type 1 (DUID-LLT) + Hardware type 1 (Ethernet) + 4-byte timestamp + 6-byte MAC
	// Captures the MAC portion (last 6 octets)
	duidLLTPattern = regexp.MustCompile(`(?i)\b00:01:00:01:[0-9a-f]{2}:[0-9a-f]{2}:[0-9a-f]{2}:[0-9a-f]{2}:([0-9a-f]{2}:[0-9a-f]{2}:[0-9a-f]{2}:[0-9a-f]{2}:[0-9a-f]{2}:[0-9a-f]{2})\b`)

	// IPv6 EUI-64 pattern: fe80::XXXX:XXff:feXX:XXXX
	// EUI-64 embeds MAC by inserting ff:fe in the middle and flipping the 7th bit
	// Captures the 4 hex groups that contain the embedded MAC
	// Pattern matches: fe80::(0-3 groups:)?XXXX:XXff:feXX:XXXX
	// Note: group2 uses {1,2} because IPv6 shorthand drops leading zeros (07ff -> 7ff)
	ipv6EUI64Pattern = regexp.MustCompile(`(?i)\bfe80::(?:[0-9a-f]{1,4}:){0,3}([0-9a-f]{1,4}):([0-9a-f]{1,2})ff:fe([0-9a-f]{2}):([0-9a-f]{1,4})\b`)

	// IPv6 pattern - used for negative matching to avoid false positives
	// Full IPv6: 8 groups of 4 hex digits separated by colons
	// We check if a potential MAC is part of a longer IPv6-like string
	ipv6FullPattern = regexp.MustCompile(`(?i)[0-9a-f]{1,4}(:[0-9a-f]{1,4}){7}`)
)

// ExtractMACs extracts all MAC addresses from a string.
// Returns normalized MACs in lowercase colon format (aa:bb:cc:dd:ee:ff).
func ExtractMACs(input string) []string {
	if input == "" {
		return nil
	}

	var macs []string
	seen := make(map[string]bool)

	// First, extract MACs from DUID patterns (these have higher priority)
	// DUID patterns use capture groups to extract just the MAC portion
	for _, pattern := range []*regexp.Regexp{duidLLPattern, duidLLTPattern} {
		matches := pattern.FindAllStringSubmatch(input, -1)
		for _, match := range matches {
			if len(match) >= 2 {
				normalized := normalizeMAC(match[1]) // match[1] is the captured MAC
				if normalized != "" && !seen[normalized] {
					seen[normalized] = true
					macs = append(macs, normalized)
				}
			}
		}
	}

	// Extract MACs from IPv6 EUI-64 link-local addresses
	eui64Matches := ipv6EUI64Pattern.FindAllStringSubmatch(input, -1)
	for _, match := range eui64Matches {
		if len(match) >= 5 {
			mac := extractMACFromEUI64(match[1], match[2], match[3], match[4])
			if mac != "" && !seen[mac] {
				seen[mac] = true
				macs = append(macs, mac)
			}
		}
	}

	// Try standard MAC patterns and collect unique MACs
	for _, pattern := range []*regexp.Regexp{macColonPattern, macDashPattern, macCiscoPattern} {
		matches := pattern.FindAllString(input, -1)
		for _, match := range matches {
			// Skip if this looks like part of an IPv6 address
			if looksLikeIPv6Part(input, match) {
				continue
			}

			// Skip if this MAC was already extracted from a DUID
			normalized := normalizeMAC(match)
			if normalized != "" && !seen[normalized] {
				seen[normalized] = true
				macs = append(macs, normalized)
			}
		}
	}

	return macs
}

// ExtractFirstMAC extracts the first MAC address found in a string.
// Returns empty string if no MAC found.
func ExtractFirstMAC(input string) string {
	macs := ExtractMACs(input)
	if len(macs) > 0 {
		return macs[0]
	}
	return ""
}

// normalizeMAC converts any MAC format to lowercase colon-separated format.
// aa:bb:cc:dd:ee:ff
func normalizeMAC(mac string) string {
	if mac == "" {
		return ""
	}

	// Remove all separators and convert to lowercase
	clean := strings.ToLower(mac)
	clean = strings.ReplaceAll(clean, ":", "")
	clean = strings.ReplaceAll(clean, "-", "")
	clean = strings.ReplaceAll(clean, ".", "")

	// Should be exactly 12 hex characters
	if len(clean) != 12 {
		return ""
	}

	// Validate all characters are hex
	for _, c := range clean {
		if !isHexDigit(c) {
			return ""
		}
	}

	// Format as aa:bb:cc:dd:ee:ff
	return formatMAC(clean)
}

// formatMAC takes 12 hex chars and formats as colon-separated MAC
func formatMAC(hexChars string) string {
	if len(hexChars) != 12 {
		return ""
	}
	return hexChars[0:2] + ":" + hexChars[2:4] + ":" + hexChars[4:6] + ":" +
		hexChars[6:8] + ":" + hexChars[8:10] + ":" + hexChars[10:12]
}

// isHexDigit checks if a rune is a valid hexadecimal digit
func isHexDigit(c rune) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

// looksLikeIPv6Part checks if the match appears to be part of an IPv6 address.
// This helps avoid false positives where a MAC-like pattern appears within IPv6.
func looksLikeIPv6Part(fullString, match string) bool {
	// Find the position of the match
	idx := strings.Index(fullString, match)
	if idx == -1 {
		return false
	}

	// Look at surrounding context - if there are more colon-separated hex groups,
	// this is likely IPv6
	contextStart := idx - 10
	if contextStart < 0 {
		contextStart = 0
	}
	contextEnd := idx + len(match) + 10
	if contextEnd > len(fullString) {
		contextEnd = len(fullString)
	}
	context := fullString[contextStart:contextEnd]

	// If the context looks like it has more than 6 hex groups separated by colons,
	// it's probably IPv6
	return ipv6FullPattern.MatchString(context)
}

// IsValidMAC checks if a string is a valid MAC address in any supported format.
func IsValidMAC(mac string) bool {
	if mac == "" {
		return false
	}
	return macColonPattern.MatchString(mac) ||
		macDashPattern.MatchString(mac) ||
		macCiscoPattern.MatchString(mac)
}

// extractMACFromEUI64 reconstructs a MAC address from EUI-64 components.
// EUI-64 format: XXXX:XXff:feXX:XXXX where ff:fe is inserted in the middle
// and the 7th bit of the first byte is flipped (Universal/Local bit).
//
// Parameters are the captured groups from the regex:
// - group1: first 1-4 hex chars (e.g., "225" from "0225")
// - group2: 1-2 hex chars before "ff" (e.g., "90" or "7" for "07ff" shortened to "7ff")
// - group3: 2 hex chars after "fe" (e.g., "8b")
// - group4: last 1-4 hex chars (e.g., "dba0")
func extractMACFromEUI64(group1, group2, group3, group4 string) string {
	// Pad group1 to 4 chars (e.g., "225" -> "0225")
	for len(group1) < 4 {
		group1 = "0" + group1
	}
	// Pad group2 to 2 chars (e.g., "7" -> "07" for "7ff" which is really "07ff")
	for len(group2) < 2 {
		group2 = "0" + group2
	}
	// Pad group4 to 4 chars
	for len(group4) < 4 {
		group4 = "0" + group4
	}

	// Parse the first byte and flip the 7th bit (Universal/Local bit)
	firstByte, err := parseHexByte(group1[0:2])
	if err != nil {
		return ""
	}
	// Flip bit 1 (0-indexed) which is the U/L bit in EUI-64
	firstByte ^= 0x02

	// Build the MAC: XX:XX:XX:XX:XX:XX
	// From: group1[0:2] group1[2:4] : group2 : group3 : group4[0:2] group4[2:4]
	mac := strings.ToLower(formatHexByte(firstByte) + ":" +
		group1[2:4] + ":" +
		group2 + ":" +
		group3 + ":" +
		group4[0:2] + ":" +
		group4[2:4])

	return mac
}

// parseHexByte parses a 2-character hex string into a byte
func parseHexByte(s string) (byte, error) {
	if len(s) != 2 {
		return 0, nil
	}
	var b byte
	for _, c := range s {
		b <<= 4
		switch {
		case c >= '0' && c <= '9':
			b |= byte(c - '0')
		case c >= 'a' && c <= 'f':
			b |= byte(c - 'a' + 10)
		case c >= 'A' && c <= 'F':
			b |= byte(c - 'A' + 10)
		default:
			return 0, nil
		}
	}
	return b, nil
}

// formatHexByte formats a byte as a 2-character lowercase hex string
func formatHexByte(b byte) string {
	const hexDigits = "0123456789abcdef"
	return string([]byte{hexDigits[b>>4], hexDigits[b&0x0f]})
}
