package cli

import (
	"bytes"
	"strings"
	"testing"
)

// The expected bytes were traced from Node 22.22.2's readline/promises
// question() on a real PTY (prompt of 31 columns), not produced by this code.
func TestWitnessReviewReadlineModel(t *testing.T) {
	const prompt = `Approve & freeze as "x"? [y/N] `
	const start = "\x1b[1G\x1b[0J" + prompt + "\x1b[32G"
	for _, c := range []struct {
		name, in, out, line string
		ok                  bool
		err                 string
	}{
		{"yes CR", "yes\r", start + "yes\r\n", "yes", true, ""},
		{"y LF", "y\n", start + "y\r\n", "y", true, ""},
		{"CRLF is one Enter", "y\r\n", start + "y\r\n", "y", true, ""},
		{"backspace redraws", "y\x7fn\r", start + "y\x1b[1G\x1b[0J" + prompt + "\x1b[32Gn\r\n", "n", true, ""},
		{"ctrl-h deletes", "y\x08\r", start + "y\x1b[1G\x1b[0J" + prompt + "\x1b[32G\r\n", "", true, ""},
		{"backspace on empty is silent", "\x7fy\r", start + "y\r\n", "y", true, ""},
		{"ctrl-d on text is ignored", "y\x04\r", start + "y\r\n", "y", true, ""},
		{"ctrl-d on empty aborts", "\x04", start, "", false, "Aborted with Ctrl+D"},
		{"ctrl-c aborts", "ab\x03", start + "ab", "", false, "Aborted with Ctrl+C"},
		{"utf-8 echo", "\xc3\xa9\r", start + "\xc3\xa9\r\n", "\xc3\xa9", true, ""},
		{"EOF settles nothing", "yes", start + "yes", "", false, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			var out bytes.Buffer
			line, ok, err := nodeReadlineQuestion(strings.NewReader(c.in), &out, prompt)
			if out.String() != c.out || line != c.line || ok != c.ok {
				t.Fatalf("out %q line %q ok %v; want %q %q %v", out.String(), line, ok, c.out, c.line, c.ok)
			}
			if (err == nil) != (c.err == "") || (err != nil && err.Error() != c.err) {
				t.Fatalf("err %v, want %q", err, c.err)
			}
		})
	}
}
