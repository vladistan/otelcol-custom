// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

// Package zfskstatreceiver reads OpenZFS kstats and emits pool I/O, pool state,
// ARC and, with datasets on, per-dataset I/O metrics. The proc source reads the
// kstat files; the sysctl source (FreeBSD) reads the kstat.zfs sysctl tree and has
// no pool I/O metrics. It runs no external command and needs no privilege beyond
// read access.
package zfskstatreceiver
