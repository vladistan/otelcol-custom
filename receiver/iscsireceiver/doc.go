// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

// Package iscsireceiver reports iSCSI initiator sessions and connections
// (iscsi.session.state, iscsi.session.count, iscsi.connection.state). It reads
// /sys/class/iscsi_session and /sys/class/iscsi_connection in process, with no
// external command, no cgo and no capability, and opens only an allow-list of
// files, never a credential file. Linux only; elsewhere the factory fails.
package iscsireceiver // import "github.com/vladistan/otelcol-custom/receiver/iscsireceiver"
