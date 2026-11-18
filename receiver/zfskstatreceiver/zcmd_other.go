// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

//go:build !freebsd

package zfskstatreceiver

// zcNameLen is MAXPATHLEN, the length of zc_name: 4096 on Linux.
const zcNameLen = 4096
