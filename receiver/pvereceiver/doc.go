// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

// Package pvereceiver reads a Proxmox VE cluster through its HTTP API with two scrapers.
// The resources scraper polls /cluster/resources and reports nodes, guests and storages as
// pve.* metrics. The guests scraper asks the QEMU guest agent of each running guest on one
// node for filesystem usage (metrics) and for the OS, host name and addresses (change-event
// logs). No external command is run and no cgo is used.
package pvereceiver // import "github.com/vladistan/otelcol-custom/receiver/pvereceiver"
