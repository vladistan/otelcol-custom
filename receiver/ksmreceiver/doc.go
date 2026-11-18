// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

// Package ksmreceiver reads the Kernel Samepage Merging counters from /sys/kernel/mm/ksm and
// emits them as linux.ksm.stats.* metrics. It opens seven files read-only and runs no command.
package ksmreceiver
