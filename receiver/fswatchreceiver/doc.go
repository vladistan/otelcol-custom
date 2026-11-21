// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

// Package fswatchreceiver implements a logs receiver that emits one record for each new file under
// watched directories, with a service name and an exclude list for each root. It calls
// ReadDirectoryChangesW directly, so it runs on Windows only; on other systems the component is
// registered and fails at startup with "platform unsupported".
package fswatchreceiver
