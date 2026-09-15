// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package ciscologprocessor

import (
	"regexp"
	"strings"
)

// CiscoLogInfo holds parsed Cisco mnemonic data
type CiscoLogInfo struct {
	Facility     string // SW_MATM, SYS, APF, COPY, ASA
	Severity     string // "0"-"7" or letter (N, I, W, E)
	Mnemonic     string // MACFLAP_NOTIF, CLOCKUPDATE, 302013
	FullMnemonic string // %SW_MATM-4-MACFLAP_NOTIF
}

// HPLogInfo holds parsed HP switch data
type HPLogInfo struct {
	Hostname     string // switch-c-1
	AppName      string // General, TRAPMGR
	ProcessID    string // tRpcsrv.00001, dot1s_task
	CodeFilepath string // traputil.c (from "traputil.c(763)")
	CodeLineno   string // 763 (from "traputil.c(763)")
	LogSequence  string // 284 (sequence number)
	CleanMessage string // Message after %% without code location prefix
}

// WLCLogInfo holds parsed Cisco WLC (Wireless LAN Controller) data
type WLCLogInfo struct {
	APName       string // DownAP-Master (the AP sending the log)
	ThreadName   string // apfMsConnTask_0 (thread/task name)
	CodeFilepath string // apf_80211.c (source file)
	CodeLineno   string // 13338 (line number)
	ClientAP     string // CiscoAP-Down (AP Name from message content)
	Radio        string // 2.4GHz, 5GHz
	WLANId       string // 1 (WLAN ID)
	CleanMessage string // Cleaned message without extracted fields
}

// Cisco mnemonic pattern: %FACILITY-SEVERITY-MNEMONIC:
// - FACILITY: uppercase letters and underscores (e.g., SW_MATM, PORT_SECURITY)
// - SEVERITY: single digit 0-7 OR single uppercase letter (N, I, W, E, etc.)
// - MNEMONIC: uppercase letters, underscores, and digits (e.g., MACFLAP_NOTIF, 302013)
// The pattern can appear anywhere in the message.
var ciscoMnemonicRegex = regexp.MustCompile(`%([A-Z][A-Z0-9_]*)-([0-7A-Z])-([A-Z0-9_]+):`)

// HP switch log pattern (with leading space before timestamp)
// Format: " Dec 15 21:31:30 hostname appname[processid]: ..."
var hpLogRegex = regexp.MustCompile(`^\s+([A-Za-z]{3}\s+\d{1,2}\s+\d{2}:\d{2}:\d{2})\s+([^\s]+)\s+([^\[]+)\[([^\]]+)\]:`)

// HP code location pattern: "filename.c(lineno) seqnum %% message"
// Example: "traputil.c(763) 284 %% Spanning Tree Topology Change Received"
var hpCodeLocationRegex = regexp.MustCompile(`\]:\s*([a-zA-Z0-9_]+\.[ch])\((\d+)\)\s+(\d+)\s+%%\s*(.*)$`)

// WLC (Wireless LAN Controller) log prefix pattern
// Format: "APName: *threadName: timestamp: %MNEMONIC: ..."
// Example: "DownAP-Master: *apfMsConnTask_0: Dec 15 23:44:44.693: %APF-5-CLIENT_ASSOCIATE:"
var wlcPrefixRegex = regexp.MustCompile(`^([A-Za-z0-9_-]+):\s+\*([A-Za-z0-9_.-]+):\s+[A-Za-z]{3}\s+\d{1,2}\s+\d{2}:\d{2}:\d{2}\.\d+:\s+`)

// WLC code location pattern after mnemonic: "filename.c:lineno message"
// Example: "apf_80211.c:13338 Client Association: ..."
var wlcCodeLocationRegex = regexp.MustCompile(`^([a-zA-Z0-9_]+\.[ch]):(\d+)\s+(.*)$`)

// WLC field extraction patterns
var wlcAPNameRegex = regexp.MustCompile(`AP Name:\s*([^,]+)`)
var wlcRadioRegex = regexp.MustCompile(`Radio:\s*([^,]+)`)
var wlcWLANIdRegex = regexp.MustCompile(`WLAN Id:\s*(\d+)`)
var wlcClientMACRegex = regexp.MustCompile(`Client MAC:\s*([a-fA-F0-9:]+)`)

// Syslog priority pattern at start of body: <digits>
var priorityRegex = regexp.MustCompile(`^<\d+>\s*`)

// ParseCiscoMnemonic finds a Cisco %FACILITY-SEV-MNEMONIC: pattern anywhere in the text.
// Returns nil if no valid Cisco mnemonic is found.
func ParseCiscoMnemonic(text string) *CiscoLogInfo {
	if text == "" {
		return nil
	}

	// Find the Cisco mnemonic pattern anywhere in the text
	match := ciscoMnemonicRegex.FindStringSubmatch(text)
	if match == nil {
		return nil
	}

	// match[0] = full match including %...:
	// match[1] = facility
	// match[2] = severity
	// match[3] = mnemonic

	return &CiscoLogInfo{
		Facility:     match[1],
		Severity:     match[2],
		Mnemonic:     match[3],
		FullMnemonic: strings.TrimSuffix(match[0], ":"),
	}
}

// ParseHPLog parses HP switch log format.
// HP logs have a leading space before the timestamp and use %% instead of Cisco %FAC-SEV-MNEM.
// Returns nil if the text doesn't match HP format.
func ParseHPLog(text string) *HPLogInfo {
	if text == "" {
		return nil
	}

	// Check for leading space (characteristic of HP logs)
	if !strings.HasPrefix(text, " ") {
		return nil
	}

	// Check for %% pattern (HP-specific, not Cisco %)
	if !strings.Contains(text, "%%") {
		return nil
	}

	match := hpLogRegex.FindStringSubmatch(text)
	if match == nil {
		return nil
	}

	// match[0] = full match
	// match[1] = timestamp
	// match[2] = hostname
	// match[3] = appname
	// match[4] = processid

	info := &HPLogInfo{
		Hostname:  match[2],
		AppName:   strings.TrimSpace(match[3]),
		ProcessID: match[4],
	}

	// Try to extract code location and sequence number
	// Format: "]: traputil.c(763) 284 %% message"
	codeMatch := hpCodeLocationRegex.FindStringSubmatch(text)
	if codeMatch != nil {
		// codeMatch[1] = filename (traputil.c)
		// codeMatch[2] = line number (763)
		// codeMatch[3] = sequence number (284)
		// codeMatch[4] = clean message
		info.CodeFilepath = codeMatch[1]
		info.CodeLineno = codeMatch[2]
		info.LogSequence = codeMatch[3]
		info.CleanMessage = codeMatch[4]
	} else {
		// No code location - extract message after %%
		if idx := strings.Index(text, "%%"); idx >= 0 {
			info.CleanMessage = strings.TrimSpace(text[idx+2:])
		}
	}

	return info
}

// ParseWLCLog parses Cisco WLC (Wireless LAN Controller) log format.
// WLC logs have a distinctive format: "APName: *thread: timestamp: %MNEMONIC: code:line message"
// Returns nil if the text doesn't match WLC format.
func ParseWLCLog(text string) *WLCLogInfo {
	if text == "" {
		return nil
	}

	// Check for WLC prefix pattern
	prefixMatch := wlcPrefixRegex.FindStringSubmatch(text)
	if prefixMatch == nil {
		return nil
	}

	info := &WLCLogInfo{
		APName:     prefixMatch[1],
		ThreadName: prefixMatch[2],
	}

	// Find content after the mnemonic
	// Look for %FACILITY-SEV-MNEMONIC: and get everything after it
	mnemonicIdx := strings.Index(text, "%")
	if mnemonicIdx < 0 {
		return info
	}

	// Find the end of mnemonic (the colon after MNEMONIC)
	afterMnemonic := text[mnemonicIdx:]
	colonIdx := strings.Index(afterMnemonic, ": ")
	if colonIdx < 0 {
		return info
	}

	content := strings.TrimSpace(afterMnemonic[colonIdx+2:])

	// Try to extract code location: "filename.c:lineno message"
	codeMatch := wlcCodeLocationRegex.FindStringSubmatch(content)
	if codeMatch != nil {
		info.CodeFilepath = codeMatch[1]
		info.CodeLineno = codeMatch[2]
		content = codeMatch[3]
	}

	// Extract WiFi-specific fields from content
	if match := wlcAPNameRegex.FindStringSubmatch(content); match != nil {
		info.ClientAP = strings.TrimSpace(match[1])
	}
	if match := wlcRadioRegex.FindStringSubmatch(content); match != nil {
		info.Radio = strings.TrimRight(strings.TrimSpace(match[1]), " .,")
	}
	if match := wlcWLANIdRegex.FindStringSubmatch(content); match != nil {
		info.WLANId = match[1]
	}

	// Build clean message by removing extracted fields
	info.CleanMessage = cleanWLCMessage(content)

	return info
}

// cleanWLCMessage removes extracted fields from WLC message content
func cleanWLCMessage(content string) string {
	// Remove "Client MAC: xx:xx:xx:xx:xx:xx, " pattern
	content = wlcClientMACRegex.ReplaceAllString(content, "")
	// Remove "AP Name: xxx, " pattern
	content = wlcAPNameRegex.ReplaceAllString(content, "")
	// Remove "Radio: xxx, " pattern (note: may have trailing space before comma)
	content = wlcRadioRegex.ReplaceAllString(content, "")
	// Remove "WLAN Id: x." pattern
	content = wlcWLANIdRegex.ReplaceAllString(content, "")

	// Clean up remaining punctuation and whitespace
	content = strings.ReplaceAll(content, ", ,", ",")
	content = strings.ReplaceAll(content, ",,", ",")
	content = strings.TrimRight(content, " ,.:")
	content = strings.TrimLeft(content, " ,")

	return strings.TrimSpace(content)
}

// IsWLCLog checks if the text looks like a WLC log format
func IsWLCLog(text string) bool {
	return wlcPrefixRegex.MatchString(text)
}

// CleanBody removes syslog priority prefix (<digits>) from the body.
// Returns the cleaned body string.
func CleanBody(body string) string {
	if body == "" {
		return ""
	}

	return priorityRegex.ReplaceAllString(body, "")
}

// Interface name patterns for various Cisco interface formats
// Order matters - longer/more specific patterns must come first
var interfacePatterns = []*regexp.Regexp{
	// Full names (longest first to avoid substring matches)
	regexp.MustCompile(`(HundredGigabitEthernet\d+(?:/\d+)+)`),
	regexp.MustCompile(`(FortyGigabitEthernet\d+(?:/\d+)+)`),
	regexp.MustCompile(`(TenGigabitEthernet\d+(?:/\d+)+)`),
	regexp.MustCompile(`(GigabitEthernet\d+(?:/\d+)+)`),
	regexp.MustCompile(`(FastEthernet\d+(?:/\d+)+)`),
	// Short names (longest first)
	regexp.MustCompile(`\b(Hu\d+(?:/\d+)+)`),
	regexp.MustCompile(`\b(Fo\d+(?:/\d+)+)`),
	regexp.MustCompile(`\b(Te\d+(?:/\d+)+)`),
	regexp.MustCompile(`\b(Gi\d+(?:/\d+)+)`),
	regexp.MustCompile(`\b(Fa\d+(?:/\d+)+)`),
	// Lowercase short names: gi1, gi4
	regexp.MustCompile(`\b(gi\d+)\b`),
	regexp.MustCompile(`\b(fa\d+)\b`),
	regexp.MustCompile(`\b(te\d+)\b`),
	// Other interface types
	regexp.MustCompile(`\b(Vlan\d+)\b`),
	regexp.MustCompile(`\b(Port-channel\d+)\b`),
	regexp.MustCompile(`\b(Loopback\d+)\b`),
	regexp.MustCompile(`\b(Tunnel\d+)\b`),
}

// ExtractInterfaceName extracts the first network interface name from text.
// Returns empty string if no interface is found.
func ExtractInterfaceName(text string) string {
	for _, pattern := range interfacePatterns {
		if match := pattern.FindStringSubmatch(text); match != nil {
			return match[1]
		}
	}
	return ""
}

// ExtractAllInterfaceNames extracts all network interface names from text.
// Returns a slice of unique interface names found.
func ExtractAllInterfaceNames(text string) []string {
	seen := make(map[string]bool)
	var interfaces []string

	for _, pattern := range interfacePatterns {
		matches := pattern.FindAllStringSubmatch(text, -1)
		for _, match := range matches {
			if !seen[match[1]] {
				seen[match[1]] = true
				interfaces = append(interfaces, match[1])
			}
		}
	}
	return interfaces
}
