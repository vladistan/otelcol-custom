// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

// Package ethtoolreceiver reports the five AWS ENA allowance counters of the
// network interfaces of the host as one sum, network.allowance.exceeded, with
// the attributes network.interface.name and allowance.
//
// The counters are read in the collector process with the SIOCETHTOOL ioctl;
// no external command and no cgo are used. In a pod the receiver sees only the
// interfaces of its network namespace, so it needs hostNetwork to see the host's
// ENA interface. The receiver builds on Linux only.
package ethtoolreceiver // import "github.com/vladistan/otelcol-custom/receiver/ethtoolreceiver"
