// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package nginx

import "testing"

func TestMatches_OnlyACombinedAccessLine(t *testing.T) {
	for _, c := range []struct {
		name, line string
		want       bool
	}{
		{"combined", `35.191.119.180 - - [20/Nov/2026:00:26:38 +0000] "POST /api/7/envelope/ HTTP/1.1" 200 41 "-" "sentry.python/2.35.0" "63.180.51.163, 34.117.68.0"`, true},
		{"json wrapped", `{"text":"10.0.0.5 - - [20/Nov/2026:00:26:38 +0000] \"GET / HTTP/1.1\" 200 2 \"-\" \"curl/8\""}`, true},
		{"influx httpd line has the same markers", `[httpd] 192.0.2.19 - mainflux [20/Nov/2026:00:02:45 +0100] "POST /write?db=zero HTTP/1.1" 204 0 "-" "InfluxDBClient" 5a1b 14684`, false},
		{"error log", `2026/11/20 00:26:38 [error] 31#31: *5 upstream timed out (110: Connection timed out)`, false},
		{"empty", "", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := Matches(c.line); got != c.want {
				t.Fatalf("Matches = %v, want %v", got, c.want)
			}
		})
	}
}
