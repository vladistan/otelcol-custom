// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

// Package iscsireceiver reports the iSCSI initiator sessions of the host:
// iscsi.session.state, iscsi.session.count and iscsi.connection.state.
//
// It reads /sys/class/iscsi_session and /sys/class/iscsi_connection in the
// collector process, with no external command, no cgo and no capability. It
// opens only an allow-list of files (target name, portal group tag, state,
// address and port) and never a credential file. The receiver runs on Linux
// only; elsewhere the factory returns an error.
package iscsireceiver // import "github.com/vladistan/otelcol-custom/receiver/iscsireceiver"
