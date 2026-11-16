// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

// Package idracreceiver reports the health, temperatures, fans, voltages,
// power, drives, memory and processors of Dell iDRAC controllers through the
// Redfish API, with the hw.* names of the OpenTelemetry semantic conventions,
// built through the hwschema module.
//
// The receiver logs in with a session token, reads with a read-only account,
// and deletes the session at shutdown. It verifies the iDRAC certificate
// (ca_file pins the CA) and never retries without verification. It speaks HTTP
// in the collector process, with no external command. Every target has its own
// hw.bmc.up series, a deadline per scrape and a back-off after a failed login.
package idracreceiver // import "github.com/vladistan/otelcol-custom/receiver/idracreceiver"
