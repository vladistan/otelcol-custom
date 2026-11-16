// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package journaldprocessor

// fieldMap names a journal field and the attribute it maps to.
type fieldMap struct{ field, attr string }

// resourceFields is ordered; the tuple key and the mapped-field set both derive from it.
var resourceFields = []fieldMap{
	{"_HOSTNAME", "host.name"},
	{"_MACHINE_ID", "host.id"},
	{"_PID", "process.pid"},
	{"_COMM", "process.command"},
	{"_SYSTEMD_UNIT", "service.name"},
}

// extendedResourceFields follow map_extended, after the five above.
var extendedResourceFields = []fieldMap{
	{"_EXE", "process.executable.path"},
	{"_CMDLINE", "process.command_line"},
	{"_UID", "user.id"},
	{"_GID", "user.group.id"},
	{"_SYSTEMD_CGROUP", "container.id"},
	{"_RUNTIME_SCOPE", "container.runtime"},
}

// extendedLogFields are set on each record, not on the resource.
var extendedLogFields = []fieldMap{
	{"_BOOT_ID", "boot.id"},
	{"_SYSTEMD_SLICE", "systemd.slice"},
	{"_SYSTEMD_INVOCATION_ID", "systemd.invocation_id"},
	{"_TRANSPORT", "log.source"},
	{"SYSLOG_FACILITY", "syslog.facility.code"},
	{"SYSLOG_IDENTIFIER", "syslog.appname"},
	{"SYSLOG_PID", "syslog.procid"},
	{"_SELINUX_CONTEXT", "selinux.context"},
	{"_CAP_EFFECTIVE", "process.capabilities.effective"},
}

// removedFromBody leave a retained map body under map_extended, after they are read.
var removedFromBody = []string{"PRIORITY", "SYSLOG_FACILITY", "SYSLOG_IDENTIFIER", "SYSLOG_PID"}

// internalFields are dropped under map_extended: they appear in no attribute and not in the body.
var internalFields = map[string]bool{
	"_SOURCE_REALTIME_TIMESTAMP": true,
	"__CURSOR":                   true,
	"__MONOTONIC_TIMESTAMP":      true,
	"_STREAM_ID":                 true,
}

// attrUnknownFields holds the keys that start with _ and that no mapping consumed.
const attrUnknownFields = "journald.unknown_fields"

// guardedAttrs are the resource attributes skip_when_resource_has protects.
var guardedAttrs = []string{"process.pid", "process.command", "service.name", "container.id"}
