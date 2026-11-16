// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

// Package megaraidreceiver reads LSI MegaRAID controllers (Dell PERC and Supermicro cards that
// use the megaraid_sas driver) through the driver's ioctl node and sends their RAID status as
// hw.* and raid.* metrics and their event log as logs.
//
// Only read-only MFI commands are sent: one function checks every frame against a fixed list of
// opcodes and, for the opt-in SMART read, a fixed list of CDBs. No megacli, storcli or smartctl is
// run, and nothing on the controller is changed. The receiver is Linux only.
package megaraidreceiver
