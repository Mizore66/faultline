package cliargs

import (
	"reflect"
	"testing"
	"unicode/utf16"
)

func TestSplitUCRT(t *testing.T) {
	for in, want := range map[string][]string{
		`fl verify b`:                 {"verify", "b"},
		`"C:\Program Files\fl.exe" x`: {"x"},
		`fl a"b"" c d`:                {`ab" c d`},
		`fl "a b" c`:                  {"a b", "c"},
		`fl a\\\"b`:                   {`a\"b`},
		`fl a\\"b c"`:                 {`a\b c`},
		`fl a\b`:                      {`a\b`},
		`fl ""`:                       {""},
		`fl  	 x  `:                   {"x"},
		`"f l" "" "`:                  {"", ""},
	} {
		var got []string
		for _, a := range SplitUCRT(utf16.Encode([]rune(in))) {
			got = append(got, string(utf16.Decode(a)))
		}
		if !reflect.DeepEqual(got, want) && !(len(got) == 0 && len(want) == 0) {
			t.Errorf("SplitUCRT(%q) = %q, want %q", in, got, want)
		}
	}
	if got := FromWide([]uint16{'a', 0xD800, 'b', 0xD83D, 0xDE00}); got != "a\ufffdb\U0001F600" {
		t.Errorf("FromWide = %q", got)
	}
}
