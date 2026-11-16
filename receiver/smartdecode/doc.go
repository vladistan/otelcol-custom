// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

// Package smartdecode decodes the buffers of SATA and NVMe health commands: IDENTIFY DEVICE, the SMART
// attribute and threshold tables, SMART RETURN STATUS, the Device Statistics log, NVMe Identify
// Controller and the NVMe SMART/Health log. It sends nothing itself. A caller gives it a transport, an
// interface with one method, and a transport can be an SG_IO call on a disk or a RAID controller layer
// that returns the same buffers. Every command passes a read-only allow list first. The package uses
// the Go standard library only and builds on every platform.
package smartdecode // import "github.com/vladistan/otelcol-custom/receiver/smartdecode"
