package main

import (
	"slices"
	"testing"
	"unicode/utf16"
)

// libuv's EOL conversion for Windows consoles (src/win/tty.c 2142-2162).
func TestConsoleUTF16(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"a\nb", "a\r\nb"},
		{"a\r\nb", "a\r\nb"},
		{"a\n\rb", "a\r\nb"},
		{"a\rb", "a\rb"},
		{"\n\n", "\r\n\r\n"},
		{"\r\n\r", "\r\n"}, // "the second \r is redundant"
		{"é😀\n", "é😀\r\n"},
	} {
		var eol rune
		if got := consoleUTF16([]byte(c.in), &eol); !slices.Equal(got, utf16.Encode([]rune(c.want))) {
			t.Errorf("%q: %q, want %q", c.in, string(utf16.Decode(got)), c.want)
		}
	}
	var eol rune
	consoleUTF16([]byte("a\n"), &eol)
	if got := consoleUTF16([]byte("\rb"), &eol); string(utf16.Decode(got)) != "b" {
		t.Errorf("a CR after an LF from the previous write: %q", string(utf16.Decode(got)))
	}
}
