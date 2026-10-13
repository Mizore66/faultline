package nodefs

import "testing"

// Absolute paths agree with path.win32.toNamespacedPath (Node 22.22.2); the
// relative and drive-relative cases follow src/path.cc PathResolve.
func TestToNamespacedPath(t *testing.T) {
	cwd := func() string { return `C:\cwd` }
	env := map[string]string{"=E:": `E:\ecwd`}
	getenv := func(k string) string { return env[k] }
	for _, tc := range []struct{ in, want, display string }{
		{`C:\a\b`, `\\?\C:\a\b`, `C:\a\b`},
		{`C:/a/../b`, `\\?\C:\b`, `C:\b`},
		{`c:\a\b.`, `\\?\c:\a\b.`, `c:\a\b.`},
		{`\\server\share\x`, `\\?\UNC\server\share\x`, `\\server\share\x`},
		{`//server/share/x/`, `\\?\UNC\server\share\x`, `\\server\share\x`},
		{`\\?\C:\x`, `\\?\C:\x`, `C:\x`},
		{`\\.\pipe\x`, `\\.\pipe\x`, `\\.\pipe\x`},
		{`C:\a\b\..\..\..`, `\\?\C:\`, `C:\`},
		{`C:\a  \b. .\NUL`, `\\?\C:\a  \b. .\NUL`, `C:\a  \b. .\NUL`},
		{`C:\`, `\\?\C:\`, `C:\`},
		{`C:\a\\\b\`, `\\?\C:\a\b`, `C:\a\b`},
		{`bundle\runs`, `\\?\C:\cwd\bundle\runs`, `C:\cwd\bundle\runs`},
		{`E:x`, `\\?\E:\ecwd\x`, `E:\ecwd\x`},
		// No =D: variable: the process cwd is on C:, but PathResolve only
		// rejects it when index 2 is '/', so C:\cwd is skipped as another
		// device and the path stays drive-relative.
		{`D:x`, `D:x`, `D:x`},
		{`C:`, `\\?\C:\cwd`, `C:\cwd`},
	} {
		got := toNamespacedPath(tc.in, cwd, getenv)
		if got != tc.want || stringFromPath(got) != tc.display {
			t.Errorf("%q: got %q (%q), want %q (%q)", tc.in, got, stringFromPath(got), tc.want, tc.display)
		}
	}
}
