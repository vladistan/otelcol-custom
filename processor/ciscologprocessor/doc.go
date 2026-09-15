// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

// Package ciscologprocessor parses Cisco and HP network device logs and extracts structured fields.
//
// This processor runs AFTER transform/syslog in the pipeline to handle cases that
// the OTTL-based parsing misses, particularly:
//   - WLC format: hostname: *task: timestamp: %FACILITY-SEV-MNEMONIC:
//   - Bare mnemonic: %FACILITY-SEV-MNEMONIC: (no seq/timestamp prefix)
//   - Letter severities: %COPY-N-TRAP (N=Notification, I=Info, W=Warning, E=Error)
//   - HP switches: RFC3164-ish format with leading space
//
// The processor finds %FACILITY-SEV-MNEMONIC: patterns anywhere in the message,
// not just at specific positions.
//
// Extracted attributes:
//   - cisco_facility: The facility code (e.g., SW_MATM, SYS, APF, COPY)
//   - cisco_mnemonic: The mnemonic code (e.g., MACFLAP_NOTIF, CLOCKUPDATE)
//   - cisco_severity: Severity (digit 0-7 or letter N/I/W/E)
//
// For HP switches:
//   - host.name: Extracted hostname
//   - service.name: Application name
//
// Additional features:
//   - Sets host.name from net.peer.name when not already set
//   - Cleans <priority> prefix from body
package ciscologprocessor // import "github.com/vladistan/otelcol-custom/processor/ciscologprocessor"
