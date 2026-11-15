// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

// Package zfskstatreceiver reads OpenZFS kstat files and emits pool I/O, pool
// state and ARC metrics. It runs no external command and needs no privilege
// beyond read access to the kstat directory.
package zfskstatreceiver
