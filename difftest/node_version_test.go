//go:build faultline_test

package difftest

import "testing"

// Catches an oracle preflight that accepts the wrong Node version bytes for a
// platform. Native `node --version` prints exactly "v22.22.2\r\n" on Windows
// (text-mode stdout) and exactly "v22.22.2\n" on Linux and Darwin; stderr must be
// empty and the exit status 0. Every expectation below is written out, never
// derived from the helper, and the table runs on any host, so one run covers all
// three platforms' branches. No trimming, no CR stripping, no accept-either.
func TestNodeVersionOutput(t *testing.T) {
	const lf, crlf = "v22.22.2\n", "v22.22.2\r\n"
	type output struct {
		name   string
		stdout string
		stderr string
		code   int
	}
	outputs := []output{
		{"exact LF", lf, "", 0},
		{"exact CRLF", crlf, "", 0},
		{"no line ending", "v22.22.2", "", 0},
		{"doubled CR", "v22.22.2\r\r\n", "", 0},
		{"leading blank line", "\nv22.22.2\n", "", 0},
		{"leading blank CRLF line", "\r\nv22.22.2\r\n", "", 0},
		{"extra newline", "v22.22.2\n\n", "", 0},
		{"extra CRLF", "v22.22.2\r\n\r\n", "", 0},
		{"leading space", " v22.22.2\n", "", 0},
		{"wrong version", "v22.22.3\n", "", 0},
		{"wrong version CRLF", "v22.22.3\r\n", "", 0},
		{"longer patch version", "v22.22.20\n", "", 0},
		{"nonempty stderr LF", lf, "warning\n", 0},
		{"nonempty stderr CRLF", crlf, "warning\r\n", 0},
		{"nonzero exit LF", lf, "", 1},
		{"nonzero exit CRLF", crlf, "", 1},
		{"negative exit LF", lf, "", -1},
		{"negative exit CRLF", crlf, "", -1},
	}
	// Per platform, the one accepted output (by name); everything else is rejected.
	accepted := map[string]string{"windows": "exact CRLF", "linux": "exact LF", "darwin": "exact LF"}
	for _, goos := range []string{"windows", "linux", "darwin"} {
		for _, o := range outputs {
			t.Run(goos+"/"+o.name, func(t *testing.T) {
				want := o.name == accepted[goos]
				if got := oracleNodeVersionOutputMatches(goos, o.stdout, o.stderr, o.code); got != want {
					t.Errorf("%s: stdout=%q stderr=%q exit=%d accepted=%v, want %v", goos, o.stdout, o.stderr, o.code, got, want)
				}
			})
		}
	}
}
