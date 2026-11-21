// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package strip

import "testing"

func TestANSI_RemovesColourCodes(t *testing.T) {
	for _, c := range []struct{ name, in, want string }{
		{"litellm colours", "\x1b[92mINFO\x1b[0m: started", "INFO: started"},
		{"rabbitmq trailing reset", "User 'x' authenticated successfully\x1b[0m", "User 'x' authenticated successfully"},
		{"bold and 256 colour", "\x1b[1;38;5;196m fail\x1b[0m", " fail"},
		{"no escape", "plain text", "plain text"},
		{"empty", "", ""},
		{"lone escape at the end", "text\x1b", "text"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := ANSI(c.in); got != c.want {
				t.Fatalf("ANSI(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestUnescape_TurnsSerialConsoleEscapesIntoText(t *testing.T) {
	for _, c := range []struct{ name, in, want string }{
		{"crlf", `[  OK  ] Started\r\n`, "[  OK  ] Started\r\n"},
		{"hex ansi", `\x1b[0;32m OK \x1b[0m`, "\x1b[0;32m OK \x1b[0m"},
		{"utf8 ellipsis", `loading\xe2\x80\xa6`, "loading\u2026"},
		{"tab", `a\tb`, "a\tb"},
		{"double backslash stays one", `C:\\dir`, `C:\dir`},
		{"bad hex stays", `\xZZ`, `\xZZ`},
		{"trailing backslash stays", `end\`, `end\`},
		{"no escape", "plain", "plain"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := Unescape(c.in); got != c.want {
				t.Fatalf("Unescape(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestSerialConsole_UnescapesStripsAndTrims(t *testing.T) {
	got := SerialConsole(`\x1b[0;32m  OK  \x1b[0m Started Docker.\r\n`)
	if want := "OK   Started Docker."; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if SerialConsole(`\r\n`) != "" {
		t.Fatalf("a bare CRLF record must come out empty")
	}
}

func TestIsBlank_WhitespaceOnlyRecords(t *testing.T) {
	for in, want := range map[string]bool{
		"":           true,
		"\r\n":       true,
		"  \t\n":     true,
		"\x1b[0m":    false,
		"a":          false,
		" a ":        false,
		"\u00a0":     true,
		"\u2026":     false,
		"\x1b[0m \n": false,
	} {
		if got := IsBlank(in); got != want {
			t.Errorf("IsBlank(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestPrefix_RemovesAWrapperPrefix(t *testing.T) {
	for _, c := range []struct{ name, in, prefix, want string }{
		{"gocd jvm wrapper", "jvm 1    | started", "jvm 1    | ", "started"},
		{"absent", "started", "jvm 1    | ", "started"},
		{"empty prefix", "started", "", "started"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := Prefix(c.in, c.prefix); got != c.want {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestTrimEOL_RemovesOnlyLineEnds(t *testing.T) {
	if got := TrimEOL("line\r\n"); got != "line" {
		t.Fatalf("got %q", got)
	}
	if got := TrimEOL("  line  \n"); got != "  line  " {
		t.Fatalf("got %q", got)
	}
}
