// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

// Package megaraidreceiver reads LSI MegaRAID controllers (megaraid_sas driver) through the
// driver's ioctl node and sends RAID status as hw.* and raid.* metrics and the event log as logs.
//
// Only read-only MFI commands are sent: one function checks every frame against a fixed list of
// opcodes and, for the SMART read, of CDBs. No external tool runs. The receiver is Linux only.
package megaraidreceiver
