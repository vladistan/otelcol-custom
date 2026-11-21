// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package syslog

import (
	"reflect"
	"testing"
)

func TestParseAs_GivesTheRecordParseGivesForTheDetectedFormat(t *testing.T) {
	for _, message := range []string{
		"Dec 31 10:05:00 alpha sshd[1]: one",
		"2026-11-20T00:03:18.148343+01:00 nas sshd[12]: Accepted publickey",
		"1 2026-11-20T00:03:18.148343+01:00 nas smartd 1517 - - Device: /dev/sda, 2 sectors",
		"123: *Dec 31 10:05:00.123: %LINK-3-UPDOWN: Interface Gi0/1, changed state to down",
		"  Dec 31 10:05:00 alpha sshd[1]: padded  ",
		"plain text that is no syslog line",
		"",
	} {
		want := Parse(message)

		got := ParseAs(message, DetectFormat(message))

		if !reflect.DeepEqual(got, want) {
			t.Errorf("%q: ParseAs = %+v, Parse = %+v", message, got, want)
		}
	}
}
