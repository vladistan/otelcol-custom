// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package nginxlogprocessor

import (
	"regexp"
	"strings"
)

// UserAgentInfo contains parsed user agent fields following OTEL semantic conventions.
type UserAgentInfo struct {
	Name    string // user_agent.name - Browser/client name
	Version string // user_agent.version - Browser/client version
	OSName  string // os.name - Operating system name
	Device  string // device.type - Device type (mobile, desktop, bot)
}

// Common bot patterns
var botPatterns = regexp.MustCompile(`(?i)(bot|crawler|spider|scraper|curl|wget|python|java|go-http|axios|node-fetch|ELB-HealthChecker|Prometheus|sentry)`)

// Browser patterns with version extraction
var browserPatterns = []struct {
	pattern *regexp.Regexp
	name    string
}{
	{regexp.MustCompile(`(?i)Chrome/(\d+[\d.]*)`), "Chrome"},
	{regexp.MustCompile(`(?i)Firefox/(\d+[\d.]*)`), "Firefox"},
	{regexp.MustCompile(`(?i)Safari/(\d+[\d.]*)`), "Safari"},
	{regexp.MustCompile(`(?i)Edge/(\d+[\d.]*)`), "Edge"},
	{regexp.MustCompile(`(?i)MSIE\s+(\d+[\d.]*)`), "Internet Explorer"},
	{regexp.MustCompile(`(?i)Opera/(\d+[\d.]*)`), "Opera"},
	{regexp.MustCompile(`(?i)sentry[.-]?(\S+)`), "Sentry SDK"},
	{regexp.MustCompile(`(?i)curl/(\d+[\d.]*)`), "curl"},
	{regexp.MustCompile(`(?i)python-requests/(\d+[\d.]*)`), "Python Requests"},
	{regexp.MustCompile(`(?i)Go-http-client/(\d+[\d.]*)`), "Go HTTP Client"},
	{regexp.MustCompile(`(?i)axios/(\d+[\d.]*)`), "Axios"},
}

// OS patterns
var osPatterns = []struct {
	pattern *regexp.Regexp
	name    string
}{
	{regexp.MustCompile(`(?i)Windows NT (\d+\.\d+)`), "Windows"},
	{regexp.MustCompile(`(?i)Mac OS X (\d+[_.\d]*)`), "macOS"},
	{regexp.MustCompile(`(?i)Linux`), "Linux"},
	{regexp.MustCompile(`(?i)Android (\d+[\d.]*)`), "Android"},
	{regexp.MustCompile(`(?i)iPhone OS (\d+[_\d]*)`), "iOS"},
	{regexp.MustCompile(`(?i)iPad.*OS (\d+[_\d]*)`), "iPadOS"},
}

// ParseUserAgent parses a user agent string and extracts structured information.
func ParseUserAgent(ua string) *UserAgentInfo {
	if ua == "" || ua == "-" {
		return nil
	}

	info := &UserAgentInfo{}

	// Check if it's a bot
	if botPatterns.MatchString(ua) {
		info.Device = "bot"
	} else if strings.Contains(strings.ToLower(ua), "mobile") ||
		strings.Contains(strings.ToLower(ua), "android") ||
		strings.Contains(strings.ToLower(ua), "iphone") {
		info.Device = "mobile"
	} else if strings.Contains(strings.ToLower(ua), "tablet") ||
		strings.Contains(strings.ToLower(ua), "ipad") {
		info.Device = "tablet"
	} else {
		info.Device = "desktop"
	}

	// Extract browser/client name and version
	for _, bp := range browserPatterns {
		if matches := bp.pattern.FindStringSubmatch(ua); matches != nil {
			info.Name = bp.name
			if len(matches) > 1 {
				info.Version = matches[1]
			}
			break
		}
	}

	// If no browser matched, try to extract something useful
	if info.Name == "" {
		// Try to get the first token as the client name
		parts := strings.SplitN(ua, "/", 2)
		if len(parts) >= 1 && len(parts[0]) < 50 {
			info.Name = strings.TrimSpace(parts[0])
			if len(parts) > 1 {
				// Extract version (first space-delimited part)
				versionParts := strings.SplitN(parts[1], " ", 2)
				if len(versionParts) >= 1 {
					info.Version = strings.TrimSpace(versionParts[0])
				}
			}
		}
	}

	// Extract OS
	for _, op := range osPatterns {
		if matches := op.pattern.FindStringSubmatch(ua); matches != nil {
			info.OSName = op.name
			break
		}
	}

	return info
}
