// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package nginx

import "strings"

// Matches reports whether the line is a combined access line the parser reads, also when journald wrapped
// it in {"text": ...}. IsNginxLog alone is loose: other access formats, such as Influx httpd lines, carry
// the same markers.
func Matches(line string) bool {
	return strings.Contains(line, "[") && ParseNginxLog(line) != nil
}
