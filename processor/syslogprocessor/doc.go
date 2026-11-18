// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

// Package syslogprocessor parses RFC3164, RFC5424 and Cisco IOS syslog lines and sets host, service,
// process id, severity and body.
// Each record gets its own resource, so records with another host or service leave under separate
// ResourceLogs. A first token ending in a colon (not an IP) is the tag, not the host; host.name then
// comes from net.peer.name.
package syslogprocessor // import "github.com/vladistan/otelcol-custom/processor/syslogprocessor"
