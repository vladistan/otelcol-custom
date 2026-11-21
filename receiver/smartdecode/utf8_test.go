// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package smartdecode

import (
	"testing"
	"unicode/utf8"
)

// putIdentifyText writes s into identify words first.. as two characters a word, the first in the high byte.
func putIdentifyText(buf []byte, first int, s string) {
	for i := 0; i+1 < len(s); i += 2 {
		w := first + i/2
		buf[w*2] = s[i+1]
		buf[w*2+1] = s[i]
	}
}

func TestParseIdentify_RawBytesInTheStringsBecomeReplacementCharacters(t *testing.T) {
	buf := make([]byte, 512)
	putIdentifyText(buf, 10, "WD-\xff\xfe123          ")
	putIdentifyText(buf, 27, "ST\x80000 model\xc3                      ")
	putIdentifyText(buf, 23, "FW\xff1    ")
	id, err := ParseIdentify(buf)
	if err != nil {
		t.Fatalf("ParseIdentify: %v", err)
	}
	for name, got := range map[string]string{"serial": id.Serial, "model": id.Model, "firmware": id.Firmware} {
		if !utf8.ValidString(got) {
			t.Errorf("%s %q is not valid UTF-8", name, got)
		}
	}
	if want := "WD-��123"; id.Serial != want {
		t.Errorf("serial = %q, want %q", id.Serial, want)
	}
	if want := "ST�000 model�"; id.Model != want {
		t.Errorf("model = %q, want %q", id.Model, want)
	}
	if want := "FW�1"; id.Firmware != want {
		t.Errorf("firmware = %q, want %q", id.Firmware, want)
	}
}

func TestParseNVMeIdentify_RawBytesInTheStringsBecomeReplacementCharacters(t *testing.T) {
	buf := make([]byte, 4096)
	copy(buf[4:24], "S\xffN\xfe0001             ")
	copy(buf[24:64], "Disk\x80 Pro")
	copy(buf[64:72], "1.0\xff")
	id, err := ParseNVMeIdentify(buf)
	if err != nil {
		t.Fatalf("ParseNVMeIdentify: %v", err)
	}
	if want := "S�N�0001"; id.Serial != want {
		t.Errorf("serial = %q, want %q", id.Serial, want)
	}
	if want := "Disk� Pro"; id.Model != want {
		t.Errorf("model = %q, want %q", id.Model, want)
	}
	if want := "1.0�"; id.Firmware != want {
		t.Errorf("firmware = %q, want %q", id.Firmware, want)
	}
}
