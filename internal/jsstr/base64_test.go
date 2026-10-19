package jsstr

import "testing"

func TestDecodeBase64Lenient(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"aGk=", "hi"},
		{"aGk", "hi"},
		{"aG k\n", "hi"},
		{"=aGk", ""},
		{"aGk=aGk=", "hi"},
		{"-_-_", "\xfb\xff\xbf"},
		{"+/+/", "\xfb\xff\xbf"},
		{"a", ""},
		{"", ""},
		{"YQ", "a"},
		{"Yő", "a"}, // two-byte strings: Node reads each unit's low byte
	} {
		if got := string(DecodeBase64Lenient(c.in)); got != c.want {
			t.Errorf("DecodeBase64Lenient(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
