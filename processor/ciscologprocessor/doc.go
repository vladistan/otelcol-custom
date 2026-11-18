// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

// Package ciscologprocessor extracts %FACILITY-SEV-MNEMONIC fields from Cisco and HP device logs that
// the OTTL syslog transform misses, and sets host.name, service.name, severity and body.
// Each record gets a resource of its own, so records with another host or service leave under separate
// ResourceLogs.
package ciscologprocessor // import "github.com/vladistan/otelcol-custom/processor/ciscologprocessor"
