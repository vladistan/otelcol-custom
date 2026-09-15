// Package ciscologreceiver implements a receiver that parses Cisco device logs
// (switches, APs, routers) with proper field extraction.
//
// Supported formats:
//   - IOS: <189>: %LINK-3-UPDOWN: Interface GigabitEthernet0/1, changed state to up
//   - NX-OS: Similar syslog format with Nexus-specific mnemonics
//   - WLC: <134>Oct 15 09:23:45 ap-office-1 kernel: wlan0: authenticated
//
// Parsed fields:
//   - cisco.facility, cisco.severity, cisco.mnemonic
//   - cisco.message, network.interface, host.name
package ciscologreceiver // import "github.com/vladistan/otelcol-custom/receiver/ciscologreceiver"

// TODO: Implement in Phase 3.4
