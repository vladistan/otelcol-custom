// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

//go:build freebsd

package zfskstatreceiver

// zcNameLen is MAXPATHLEN, the length of zc_name: 1024 on FreeBSD.
const zcNameLen = 1024
