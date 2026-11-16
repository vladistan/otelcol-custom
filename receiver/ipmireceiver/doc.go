// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

// Package ipmireceiver reports the sensors of baseboard management
// controllers (BMCs) over IPMI with the hw.* names of the OpenTelemetry
// semantic conventions, built through the hwschema module: temperatures with
// their upper thresholds, fans, voltages, power supply and chassis intrusion
// states, and optionally the host power from DCMI.
//
// The receiver speaks IPMI in the collector process through go-ipmi, with no
// external command. It is built for a Deployment of its own, one goroutine per
// BMC: each scrape opens one session, reads and closes it, never probes cipher
// suites, and backs a BMC off after a failed login so a shared account is not
// locked out. Every BMC has its own hw.bmc.up series.
package ipmireceiver // import "github.com/vladistan/otelcol-custom/receiver/ipmireceiver"
